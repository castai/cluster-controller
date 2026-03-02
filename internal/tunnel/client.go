package tunnel

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"k8s.io/client-go/rest"

	"github.com/castai/cluster-controller/internal/tunnel/pb"
	"github.com/castai/cluster-controller/internal/waitext"

	_ "google.golang.org/grpc/encoding/gzip"
)

const (
	defaultKubeURL      = "https://kubernetes.default.svc"
	streamChunkSize     = 32 * 1024
	maxConcurrentProxy  = 50

	metadataKeyClusterID  = "x-cluster-id"
	metadataKeyRequestID  = "x-request-id"
	metadataKeyAuthHeader = "authorization"
)

type Config struct {
	Address   string
	ClusterID string
	APIKey    string
	TLSCACert string
}

type Client struct {
	log        logrus.FieldLogger
	cfg        Config
	httpClient *http.Client
	kubeURL    string
	connected  atomic.Bool
}

func NewClient(log logrus.FieldLogger, cfg Config, restCfg *rest.Config) (*Client, error) {
	transport, err := rest.TransportFor(restCfg)
	if err != nil {
		return nil, fmt.Errorf("creating kube transport: %w", err)
	}

	return &Client{
		log: log,
		cfg: cfg,
		httpClient: &http.Client{
			Transport: transport,
		},
		kubeURL: defaultKubeURL,
	}, nil
}

func (c *Client) Run(ctx context.Context) error {
	dialOpts, err := c.grpcDialOptions()
	if err != nil {
		return fmt.Errorf("creating dial options: %w", err)
	}

	conn, err := grpc.NewClient(c.cfg.Address, dialOpts...)
	if err != nil {
		return fmt.Errorf("dialing gRPC server: %w", err)
	}
	defer conn.Close()

	client := pb.NewClusterTunnelClient(conn)

	for {
		boff := waitext.DefaultExponentialBackoff()
		err := waitext.Retry(ctx, boff, waitext.Forever, func(ctx context.Context) (bool, error) {
			if err := c.subscribe(ctx, client); err != nil {
				if ctx.Err() == nil {
					c.log.WithError(err).Warn("tunnel subscription failed, retrying")
				}
				return true, err
			}
			return false, nil
		}, nil)

		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			c.log.WithError(err).Error("tunnel retry loop exited unexpectedly, restarting in 5s")
			select {
			case <-time.After(5 * time.Second):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

func (c *Client) IsConnected() bool {
	return c.connected.Load()
}

func (c *Client) grpcDialOptions() ([]grpc.DialOption, error) {
	var opts []grpc.DialOption

	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}
	if c.cfg.TLSCACert != "" {
		certPool := x509.NewCertPool()
		if !certPool.AppendCertsFromPEM([]byte(c.cfg.TLSCACert)) {
			return nil, fmt.Errorf("failed to add CA certificate")
		}
		tlsCfg.RootCAs = certPool
	}
	opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)))

	opts = append(opts, grpc.WithKeepaliveParams(keepalive.ClientParameters{
		Time:                30 * time.Second,
		Timeout:             10 * time.Second,
		PermitWithoutStream: true,
	}))

	opts = append(opts, grpc.WithDefaultCallOptions(grpc.UseCompressor("gzip")))

	opts = append(opts,
		grpc.WithUnaryInterceptor(c.authUnaryInterceptor),
		grpc.WithStreamInterceptor(c.authStreamInterceptor),
	)

	return opts, nil
}

func (c *Client) withAuth(ctx context.Context) context.Context {
	return metadata.AppendToOutgoingContext(ctx,
		metadataKeyClusterID, c.cfg.ClusterID,
		metadataKeyAuthHeader, "Token "+c.cfg.APIKey,
	)
}

func (c *Client) authUnaryInterceptor(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	return invoker(c.withAuth(ctx), method, req, reply, cc, opts...)
}

func (c *Client) authStreamInterceptor(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
	return streamer(c.withAuth(ctx), desc, cc, method, opts...)
}

func (c *Client) subscribe(ctx context.Context, client pb.ClusterTunnelClient) error {
	stream, err := client.Subscribe(ctx, &pb.SubscribeRequest{})
	if err != nil {
		return fmt.Errorf("opening subscribe stream: %w", err)
	}

	c.connected.Store(true)
	defer c.connected.Store(false)

	c.log.Info("tunnel connected")

	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(maxConcurrentProxy)

	var recvErr error
	for {
		req, err := stream.Recv()
		if err != nil {
			recvErr = err
			break
		}

		g.Go(func() error {
			c.proxyRequest(ctx, client, req)
			return nil
		})
	}

	_ = g.Wait()

	return fmt.Errorf("subscribe stream ended: %w", recvErr)
}

func (c *Client) proxyRequest(ctx context.Context, client pb.ClusterTunnelClient, req *pb.HttpRequest) {
	log := c.log.WithFields(logrus.Fields{
		"request_id": req.GetRequestId(),
		"method":     req.GetMethod(),
		"path":       req.GetPath(),
	})

	if err := c.validateRequest(req); err != nil {
		log.WithError(err).Warn("request rejected")
		c.sendErrorResponse(client, req.GetRequestId(), http.StatusForbidden, err.Error())
		return
	}

	httpReq, err := http.NewRequestWithContext(ctx, req.GetMethod(), c.kubeURL+req.GetPath(), nil)
	if err != nil {
		c.sendErrorResponse(client, req.GetRequestId(), http.StatusBadGateway, fmt.Sprintf("creating request: %v", err))
		return
	}

	if len(req.GetBody()) > 0 {
		httpReq.Body = io.NopCloser(bytes.NewReader(req.GetBody()))
		httpReq.ContentLength = int64(len(req.GetBody()))
	}

	for _, h := range req.GetHeaders() {
		if !isAllowedHeader(h.GetKey()) {
			continue
		}
		httpReq.Header.Add(h.GetKey(), h.GetValue())
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		c.sendErrorResponse(client, req.GetRequestId(), http.StatusBadGateway, fmt.Sprintf("executing request: %v", err))
		return
	}
	defer resp.Body.Close()

	log.WithField("status", resp.StatusCode).Info("proxied request")

	md := metadata.Pairs(metadataKeyRequestID, req.GetRequestId())
	streamCtx := metadata.NewOutgoingContext(ctx, md)

	respStream, err := client.SendResponse(streamCtx)
	if err != nil {
		log.WithError(err).Error("opening SendResponse stream")
		return
	}

	var headers []*pb.Header
	for k, vals := range resp.Header {
		for _, v := range vals {
			headers = append(headers, &pb.Header{Key: k, Value: v})
		}
	}

	if err := respStream.Send(&pb.HttpResponse{
		StatusCode: int32(resp.StatusCode),
		Headers:    headers,
	}); err != nil {
		log.WithError(err).Error("sending response headers")
		return
	}

	buf := make([]byte, streamChunkSize)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if err := respStream.Send(&pb.HttpResponse{
				Body: buf[:n],
			}); err != nil {
				log.WithError(err).Error("sending response body chunk")
				return
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			log.WithError(readErr).Warn("reading response body")
			break
		}
	}

	if _, err := respStream.CloseAndRecv(); err != nil {
		log.WithError(err).Warn("closing SendResponse stream")
	}
}

var blockedSubresources = map[string]bool{
	"exec":                true,
	"attach":              true,
	"portforward":         true,
	"proxy":               true,
	"token":               true,
	"ephemeralcontainers": true,
}

func (c *Client) validateRequest(req *pb.HttpRequest) error {
	if req.GetMethod() != http.MethodGet {
		return fmt.Errorf("method %s not allowed", req.GetMethod())
	}

	path, err := url.PathUnescape(req.GetPath())
	if err != nil {
		return fmt.Errorf("invalid path encoding: %w", err)
	}
	if !strings.HasPrefix(path, "/") {
		return fmt.Errorf("path must start with /")
	}

	if sub := extractSubresource(path); blockedSubresources[sub] {
		return fmt.Errorf("subresource %q not allowed", sub)
	}

	return nil
}

// extractSubresource parses a K8s API path and returns the subresource segment
// if present. The K8s API path structure after stripping the prefix is:
//
//	/api/v1/{resource}                                          → list (cluster-scoped)
//	/api/v1/{resource}/{name}                                   → get (cluster-scoped)
//	/api/v1/{resource}/{name}/{subresource}                     → subresource (cluster-scoped)
//	/api/v1/namespaces/{ns}/{resource}                          → list (namespaced)
//	/api/v1/namespaces/{ns}/{resource}/{name}                   → get (namespaced)
//	/api/v1/namespaces/{ns}/{resource}/{name}/{subresource}     → subresource (namespaced)
//	/apis/{group}/{version}/... same patterns                   → named API groups
func extractSubresource(path string) string {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")

	// Strip API prefix: "api/v1/..." or "apis/{group}/{version}/..."
	switch {
	case len(parts) >= 2 && parts[0] == "api":
		parts = parts[2:] // skip "api", version
	case len(parts) >= 3 && parts[0] == "apis":
		parts = parts[3:] // skip "apis", group, version
	default:
		return ""
	}

	// Strip "namespaces/{ns}" if present.
	if len(parts) >= 2 && parts[0] == "namespaces" {
		parts = parts[2:]
	}

	// parts is now: [resource], [resource, name], or [resource, name, subresource, ...]
	if len(parts) >= 3 {
		return parts[2]
	}
	return ""
}

var allowedForwardHeaders = map[string]bool{
	"Accept":          true,
	"Accept-Encoding": true,
	"Content-Type":    true,
	"Content-Length":  true,
	"X-Request-Id":    true,
	"Cache-Control":   true,
}

func isAllowedHeader(key string) bool {
	return allowedForwardHeaders[http.CanonicalHeaderKey(key)]
}

func (c *Client) sendErrorResponse(client pb.ClusterTunnelClient, requestID string, statusCode int32, errMsg string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	md := metadata.Pairs(metadataKeyRequestID, requestID)
	streamCtx := metadata.NewOutgoingContext(ctx, md)

	respStream, err := client.SendResponse(streamCtx)
	if err != nil {
		c.log.WithError(err).Error("opening SendResponse stream for error")
		return
	}

	_ = respStream.Send(&pb.HttpResponse{
		StatusCode: statusCode,
		Body:       []byte(errMsg),
	})

	_, _ = respStream.CloseAndRecv()
}
