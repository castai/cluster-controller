package tunnel

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"github.com/castai/cluster-controller/internal/tunnel/pb"
)

func TestProxyRequest(t *testing.T) {
	t.Run("GET round-trip", func(t *testing.T) {
		kubeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/v1/namespaces", r.URL.Path)
			assert.Equal(t, http.MethodGet, r.Method)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"kind":"NamespaceList"}`)
		}))
		defer kubeServer.Close()

		resp := proxyViaTestServer(t, kubeServer, &pb.HttpRequest{
			RequestId: "req-1",
			Method:    http.MethodGet,
			Path:      "/api/v1/namespaces",
		})

		assert.Equal(t, int32(http.StatusOK), resp.statusCode)
		assert.Contains(t, string(resp.body), "NamespaceList")
		assert.Equal(t, "req-1", resp.requestID)
	})

	t.Run("POST with body round-trip", func(t *testing.T) {
		kubeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			w.WriteHeader(http.StatusCreated)
			w.Write(body)
		}))
		defer kubeServer.Close()

		resp := proxyViaTestServer(t, kubeServer, &pb.HttpRequest{
			RequestId: "req-2",
			Method:    http.MethodPost,
			Path:      "/api/v1/namespaces",
			Body:      []byte(`{"metadata":{"name":"test"}}`),
			Headers: []*pb.Header{
				{Key: "Content-Type", Value: "application/json"},
			},
		})

		assert.Equal(t, int32(http.StatusForbidden), resp.statusCode)
		assert.Contains(t, string(resp.body), "not allowed")
	})

	t.Run("multi-value headers are preserved", func(t *testing.T) {
		kubeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, []string{"val1", "val2"}, r.Header.Values("X-Custom"))
			w.Header().Add("X-Response", "r1")
			w.Header().Add("X-Response", "r2")
			w.WriteHeader(http.StatusOK)
		}))
		defer kubeServer.Close()

		resp := proxyViaTestServer(t, kubeServer, &pb.HttpRequest{
			RequestId: "req-3",
			Method:    http.MethodGet,
			Path:      "/test",
			Headers: []*pb.Header{
				{Key: "X-Custom", Value: "val1"},
				{Key: "X-Custom", Value: "val2"},
			},
		})

		assert.Equal(t, int32(http.StatusOK), resp.statusCode)
		assert.ElementsMatch(t, []string{"r1", "r2"}, resp.headers["X-Response"])
	})

	t.Run("kube server error returns 502", func(t *testing.T) {
		kubeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		kubeServer.Close()

		resp := proxyViaTestServer(t, kubeServer, &pb.HttpRequest{
			RequestId: "req-4",
			Method:    http.MethodGet,
			Path:      "/api/v1/pods",
		})

		assert.Equal(t, int32(http.StatusBadGateway), resp.statusCode)
	})

	t.Run("streaming response sends multiple body chunks", func(t *testing.T) {
		kubeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			flusher, ok := w.(http.Flusher)
			if !ok {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
			for i := range 3 {
				fmt.Fprintf(w, "chunk-%d\n", i)
				flusher.Flush()
			}
		}))
		defer kubeServer.Close()

		resp := proxyViaTestServer(t, kubeServer, &pb.HttpRequest{
			RequestId: "req-stream",
			Method:    http.MethodGet,
			Path:      "/logs",
		})

		assert.Equal(t, int32(http.StatusOK), resp.statusCode)
		assert.Contains(t, string(resp.body), "chunk-0")
		assert.Contains(t, string(resp.body), "chunk-1")
		assert.Contains(t, string(resp.body), "chunk-2")
	})
}

func TestStreamHandling(t *testing.T) {
	t.Run("concurrent requests correlated by x-request-id metadata", func(t *testing.T) {
		kubeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "path=%s", r.URL.Path)
		}))
		defer kubeServer.Close()

		const numRequests = 10

		var mu sync.Mutex
		responses := make(map[string]*collectedResponse)

		srv, addr := startTestServer(t, testServerHandlers{
			subscribeHandler: func(_ *pb.SubscribeRequest, stream grpc.ServerStreamingServer[pb.HttpRequest]) error {
				for i := range numRequests {
					if err := stream.Send(&pb.HttpRequest{
						RequestId: fmt.Sprintf("req-%d", i),
						Method:    http.MethodGet,
						Path:      fmt.Sprintf("/api/v1/ns/%d", i),
					}); err != nil {
						return err
					}
				}
				<-stream.Context().Done()
				return nil
			},
			sendResponseHandler: func(stream grpc.ClientStreamingServer[pb.HttpResponse, pb.SendResponseResult]) error {
				resp := collectResponse(t, stream)
				mu.Lock()
				responses[resp.requestID] = resp
				mu.Unlock()
				return nil
			},
		})
		defer srv.GracefulStop()

		c := &Client{
			log:        logrus.New(),
			cfg:        Config{},
			httpClient: kubeServer.Client(),
			kubeURL:    kubeServer.URL,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		require.NoError(t, err)
		defer conn.Close()

		client := pb.NewClusterTunnelClient(conn)
		_ = c.subscribe(ctx, client)

		mu.Lock()
		defer mu.Unlock()
		for i := range numRequests {
			id := fmt.Sprintf("req-%d", i)
			expected := fmt.Sprintf("path=/api/v1/ns/%d", i)
			resp, ok := responses[id]
			require.True(t, ok, "missing response for %s", id)
			assert.Equal(t, expected, string(resp.body))
		}
	})
}

func TestRequestValidation(t *testing.T) {
	t.Run("non-GET method rejected with 403", func(t *testing.T) {
		kubeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("should not reach kube server")
		}))
		defer kubeServer.Close()

		resp := proxyViaTestServer(t, kubeServer, &pb.HttpRequest{
			RequestId: "req-post",
			Method:    http.MethodPost,
			Path:      "/api/v1/namespaces",
		})

		assert.Equal(t, int32(http.StatusForbidden), resp.statusCode)
		assert.Contains(t, string(resp.body), "not allowed")
	})

	t.Run("path with /exec rejected with 403", func(t *testing.T) {
		kubeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("should not reach kube server")
		}))
		defer kubeServer.Close()

		resp := proxyViaTestServer(t, kubeServer, &pb.HttpRequest{
			RequestId: "req-exec",
			Method:    http.MethodGet,
			Path:      "/api/v1/namespaces/default/pods/foo/exec",
		})

		assert.Equal(t, int32(http.StatusForbidden), resp.statusCode)
		assert.Contains(t, string(resp.body), "not allowed")
	})

	t.Run("path with /attach rejected with 403", func(t *testing.T) {
		kubeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("should not reach kube server")
		}))
		defer kubeServer.Close()

		resp := proxyViaTestServer(t, kubeServer, &pb.HttpRequest{
			RequestId: "req-attach",
			Method:    http.MethodGet,
			Path:      "/api/v1/namespaces/default/pods/foo/attach",
		})

		assert.Equal(t, int32(http.StatusForbidden), resp.statusCode)
		assert.Contains(t, string(resp.body), "not allowed")
	})

	t.Run("path with /portforward rejected with 403", func(t *testing.T) {
		kubeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("should not reach kube server")
		}))
		defer kubeServer.Close()

		resp := proxyViaTestServer(t, kubeServer, &pb.HttpRequest{
			RequestId: "req-pf",
			Method:    http.MethodGet,
			Path:      "/api/v1/namespaces/default/pods/foo/portforward",
		})

		assert.Equal(t, int32(http.StatusForbidden), resp.statusCode)
		assert.Contains(t, string(resp.body), "not allowed")
	})

	t.Run("path without leading slash rejected with 403", func(t *testing.T) {
		kubeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("should not reach kube server")
		}))
		defer kubeServer.Close()

		resp := proxyViaTestServer(t, kubeServer, &pb.HttpRequest{
			RequestId: "req-no-slash",
			Method:    http.MethodGet,
			Path:      "api/v1/pods",
		})

		assert.Equal(t, int32(http.StatusForbidden), resp.statusCode)
		assert.Contains(t, string(resp.body), "must start with /")
	})

	t.Run("pod name containing exec substring is allowed", func(t *testing.T) {
		kubeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"kind":"Pod"}`)
		}))
		defer kubeServer.Close()

		resp := proxyViaTestServer(t, kubeServer, &pb.HttpRequest{
			RequestId: "req-exec-pod",
			Method:    http.MethodGet,
			Path:      "/api/v1/namespaces/default/pods/exec-worker/logs",
		})

		assert.Equal(t, int32(http.StatusOK), resp.statusCode)
	})
}

func TestAuthMetadata(t *testing.T) {
	t.Run("auth metadata present on Subscribe and SendResponse", func(t *testing.T) {
		kubeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer kubeServer.Close()

		var subscribeClusterID, subscribeAuth string
		var sendResponseClusterID, sendResponseAuth string
		var mu sync.Mutex

		srv, addr := startTestServer(t, testServerHandlers{
			subscribeHandler: func(_ *pb.SubscribeRequest, stream grpc.ServerStreamingServer[pb.HttpRequest]) error {
				md, _ := metadata.FromIncomingContext(stream.Context())
				if vals := md.Get(metadataKeyClusterID); len(vals) > 0 {
					subscribeClusterID = vals[0]
				}
				if vals := md.Get(metadataKeyAuthHeader); len(vals) > 0 {
					subscribeAuth = vals[0]
				}

				if err := stream.Send(&pb.HttpRequest{
					RequestId: "req-auth",
					Method:    http.MethodGet,
					Path:      "/healthz",
				}); err != nil {
					return err
				}
				<-stream.Context().Done()
				return nil
			},
			sendResponseHandler: func(stream grpc.ClientStreamingServer[pb.HttpResponse, pb.SendResponseResult]) error {
				md, _ := metadata.FromIncomingContext(stream.Context())
				mu.Lock()
				if vals := md.Get(metadataKeyClusterID); len(vals) > 0 {
					sendResponseClusterID = vals[0]
				}
				if vals := md.Get(metadataKeyAuthHeader); len(vals) > 0 {
					sendResponseAuth = vals[0]
				}
				mu.Unlock()

				for {
					_, err := stream.Recv()
					if err != nil {
						break
					}
				}
				return stream.SendAndClose(&pb.SendResponseResult{})
			},
		})
		defer srv.GracefulStop()

		c := &Client{
			log: logrus.New(),
			cfg: Config{
				ClusterID: "test-cluster-123",
				APIKey:    "test-api-key",
			},
			httpClient: kubeServer.Client(),
			kubeURL:    kubeServer.URL,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		conn, err := grpc.NewClient(addr,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithUnaryInterceptor(c.authUnaryInterceptor),
			grpc.WithStreamInterceptor(c.authStreamInterceptor),
		)
		require.NoError(t, err)
		defer conn.Close()

		client := pb.NewClusterTunnelClient(conn)
		_ = c.subscribe(ctx, client)

		assert.Equal(t, "test-cluster-123", subscribeClusterID)
		assert.Equal(t, "Token test-api-key", subscribeAuth)

		mu.Lock()
		defer mu.Unlock()
		assert.Equal(t, "test-cluster-123", sendResponseClusterID)
		assert.Equal(t, "Token test-api-key", sendResponseAuth)
	})
}

// Test helpers

type collectedResponse struct {
	requestID  string
	statusCode int32
	headers    map[string][]string
	body       []byte
}

type testServerHandlers struct {
	subscribeHandler    func(*pb.SubscribeRequest, grpc.ServerStreamingServer[pb.HttpRequest]) error
	sendResponseHandler func(grpc.ClientStreamingServer[pb.HttpResponse, pb.SendResponseResult]) error
}

type testServer struct {
	pb.UnimplementedClusterTunnelServer
	handlers testServerHandlers
}

func (s *testServer) Subscribe(req *pb.SubscribeRequest, stream grpc.ServerStreamingServer[pb.HttpRequest]) error {
	if s.handlers.subscribeHandler != nil {
		return s.handlers.subscribeHandler(req, stream)
	}
	return nil
}

func (s *testServer) SendResponse(stream grpc.ClientStreamingServer[pb.HttpResponse, pb.SendResponseResult]) error {
	if s.handlers.sendResponseHandler != nil {
		return s.handlers.sendResponseHandler(stream)
	}
	for {
		_, err := stream.Recv()
		if err != nil {
			break
		}
	}
	return stream.SendAndClose(&pb.SendResponseResult{})
}

func collectResponse(t *testing.T, stream grpc.ClientStreamingServer[pb.HttpResponse, pb.SendResponseResult]) *collectedResponse {
	t.Helper()

	md, _ := metadata.FromIncomingContext(stream.Context())
	var requestID string
	if vals := md.Get(metadataKeyRequestID); len(vals) > 0 {
		requestID = vals[0]
	}

	resp := &collectedResponse{
		requestID: requestID,
		headers:   make(map[string][]string),
	}
	var buf bytes.Buffer

	for {
		msg, err := stream.Recv()
		if err != nil {
			break
		}
		if msg.GetStatusCode() != 0 {
			resp.statusCode = msg.GetStatusCode()
		}
		for _, h := range msg.GetHeaders() {
			resp.headers[h.GetKey()] = append(resp.headers[h.GetKey()], h.GetValue())
		}
		if len(msg.GetBody()) > 0 {
			buf.Write(msg.GetBody())
		}
	}

	resp.body = buf.Bytes()
	_ = stream.SendAndClose(&pb.SendResponseResult{})
	return resp
}

func proxyViaTestServer(t *testing.T, kubeServer *httptest.Server, req *pb.HttpRequest) *collectedResponse {
	t.Helper()

	var mu sync.Mutex
	var result *collectedResponse

	srv, addr := startTestServer(t, testServerHandlers{
		subscribeHandler: func(_ *pb.SubscribeRequest, stream grpc.ServerStreamingServer[pb.HttpRequest]) error {
			if err := stream.Send(req); err != nil {
				return err
			}
			<-stream.Context().Done()
			return nil
		},
		sendResponseHandler: func(stream grpc.ClientStreamingServer[pb.HttpResponse, pb.SendResponseResult]) error {
			resp := collectResponse(t, stream)
			mu.Lock()
			result = resp
			mu.Unlock()
			return nil
		},
	})
	defer srv.GracefulStop()

	c := &Client{
		log:        logrus.New(),
		cfg:        Config{},
		httpClient: kubeServer.Client(),
		kubeURL:    kubeServer.URL,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()

	client := pb.NewClusterTunnelClient(conn)
	_ = c.subscribe(ctx, client)

	mu.Lock()
	defer mu.Unlock()
	require.NotNil(t, result, "no response collected")
	return result
}

func startTestServer(t *testing.T, handlers testServerHandlers) (*grpc.Server, string) {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	srv := grpc.NewServer()
	pb.RegisterClusterTunnelServer(srv, &testServer{handlers: handlers})

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := srv.Serve(lis); err != nil {
			t.Logf("test server stopped: %v", err)
		}
	}()

	t.Cleanup(func() {
		srv.GracefulStop()
		wg.Wait()
	})

	return srv, lis.Addr().String()
}
