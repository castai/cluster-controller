package tunnel

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"k8s.io/client-go/rest"

	"github.com/castai/cluster-controller/internal/tunnel/pb"
	"github.com/castai/cluster-controller/internal/waitext"
)

const (
	defaultHeartbeatInterval = 30 * time.Second
	defaultKubeURL           = "https://kubernetes.default.svc"
	streamChunkSize          = 32 * 1024 // 32KB

	metadataKeyClusterID = "x-cluster-id"
)

type Config struct {
	Address           string
	ClusterID         string
	TLSCACert         string
	HeartbeatInterval time.Duration
}

type Client struct {
	log        logrus.FieldLogger
	cfg        Config
	httpClient *http.Client
	kubeURL    string

	mu        sync.Mutex
	connected bool
}

func NewClient(log logrus.FieldLogger, cfg Config, restCfg *rest.Config) (*Client, error) {
	transport, err := rest.TransportFor(restCfg)
	if err != nil {
		return nil, fmt.Errorf("creating kube transport: %w", err)
	}

	if cfg.HeartbeatInterval == 0 {
		cfg.HeartbeatInterval = defaultHeartbeatInterval
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
	for {
		boff := waitext.DefaultExponentialBackoff()
		err := waitext.Retry(ctx, boff, waitext.Forever, func(ctx context.Context) (bool, error) {
			if err := c.connect(ctx); err != nil {
				c.log.WithError(err).Warn("tunnel connection failed, retrying")
				return true, err
			}
			return false, nil
		}, nil)

		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			c.log.WithError(err).Error("tunnel retry loop exited unexpectedly")
		}
	}
}

func (c *Client) IsConnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

func (c *Client) setConnected(v bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.connected = v
}

func (c *Client) connect(ctx context.Context) error {
	dialOpts, err := c.grpcDialOptions()
	if err != nil {
		return fmt.Errorf("creating dial options: %w", err)
	}

	conn, err := grpc.NewClient(c.cfg.Address, dialOpts...)
	if err != nil {
		return fmt.Errorf("dialing gRPC server: %w", err)
	}
	defer conn.Close()

	md := metadata.New(map[string]string{
		metadataKeyClusterID: c.cfg.ClusterID,
	})
	streamCtx := metadata.NewOutgoingContext(ctx, md)

	client := pb.NewClusterTunnelClient(conn)
	stream, err := client.Connect(streamCtx)
	if err != nil {
		return fmt.Errorf("opening stream: %w", err)
	}

	c.setConnected(true)
	defer c.setConnected(false)

	c.log.Info("tunnel connected")
	return c.handleStream(ctx, stream)
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

	return opts, nil
}

func (c *Client) handleStream(ctx context.Context, stream pb.ClusterTunnel_ConnectClient) error {
	sw := &streamWriter{stream: stream}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go c.heartbeatLoop(ctx, sw)

	for {
		msg, err := stream.Recv()
		if err != nil {
			return fmt.Errorf("receiving message: %w", err)
		}

		req := msg.GetHttpRequest()
		if req == nil {
			continue
		}

		go c.proxyRequest(ctx, sw, req)
	}
}

func (c *Client) heartbeatLoop(ctx context.Context, sw *streamWriter) {
	ticker := time.NewTicker(c.cfg.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			msg := &pb.TunnelMessage{
				Payload: &pb.TunnelMessage_Heartbeat{
					Heartbeat: &pb.Heartbeat{TimestampMs: time.Now().UnixMilli()},
				},
			}
			if err := sw.Send(msg); err != nil {
				c.log.WithError(err).Warn("sending heartbeat")
				return
			}
		}
	}
}

func (c *Client) proxyRequest(ctx context.Context, sw *streamWriter, req *pb.HttpRequest) {
	c.doProxy(ctx, sw, c.kubeURL, req)
}

func (c *Client) doProxy(ctx context.Context, sw *streamWriter, baseURL string, req *pb.HttpRequest) {
	log := c.log.WithField("request_id", req.RequestId)

	httpReq, err := http.NewRequestWithContext(ctx, req.Method, baseURL+req.Path, nil)
	if err != nil {
		c.sendErrorResponse(sw, req.RequestId, http.StatusBadGateway, fmt.Sprintf("creating request: %v", err))
		return
	}

	if len(req.Body) > 0 {
		httpReq.Body = io.NopCloser(bytes.NewReader(req.Body))
		httpReq.ContentLength = int64(len(req.Body))
	}

	for _, h := range req.Headers {
		httpReq.Header.Add(h.Key, h.Value)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		c.sendErrorResponse(sw, req.RequestId, http.StatusBadGateway, fmt.Sprintf("executing request: %v", err))
		return
	}
	defer resp.Body.Close()

	var headers []*pb.Header
	for k, vals := range resp.Header {
		for _, v := range vals {
			headers = append(headers, &pb.Header{Key: k, Value: v})
		}
	}

	if err := sw.Send(&pb.TunnelMessage{
		Payload: &pb.TunnelMessage_HttpResponseStart{
			HttpResponseStart: &pb.HttpResponseStart{
				RequestId:  req.RequestId,
				StatusCode: int32(resp.StatusCode),
				Headers:    headers,
			},
		},
	}); err != nil {
		log.WithError(err).Error("sending response start")
		return
	}

	buf := make([]byte, streamChunkSize)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if err := sw.Send(&pb.TunnelMessage{
				Payload: &pb.TunnelMessage_HttpResponseBody{
					HttpResponseBody: &pb.HttpResponseBody{
						RequestId: req.RequestId,
						Data:      buf[:n],
					},
				},
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

	if err := sw.Send(&pb.TunnelMessage{
		Payload: &pb.TunnelMessage_HttpResponseEnd{
			HttpResponseEnd: &pb.HttpResponseEnd{
				RequestId: req.RequestId,
			},
		},
	}); err != nil {
		log.WithError(err).Error("sending response end")
	}
}

func (c *Client) sendErrorResponse(sw *streamWriter, requestID string, statusCode int32, errMsg string) {
	_ = sw.Send(&pb.TunnelMessage{
		Payload: &pb.TunnelMessage_HttpResponseStart{
			HttpResponseStart: &pb.HttpResponseStart{
				RequestId:  requestID,
				StatusCode: statusCode,
			},
		},
	})
	_ = sw.Send(&pb.TunnelMessage{
		Payload: &pb.TunnelMessage_HttpResponseBody{
			HttpResponseBody: &pb.HttpResponseBody{
				RequestId: requestID,
				Data:      []byte(errMsg),
			},
		},
	})
	_ = sw.Send(&pb.TunnelMessage{
		Payload: &pb.TunnelMessage_HttpResponseEnd{
			HttpResponseEnd: &pb.HttpResponseEnd{
				RequestId: requestID,
			},
		},
	})
}

type streamWriter struct {
	mu     sync.Mutex
	stream pb.ClusterTunnel_ConnectClient
}

func (sw *streamWriter) Send(msg *pb.TunnelMessage) error {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	return sw.stream.Send(msg)
}
