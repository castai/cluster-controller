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

	t.Run("allowed headers are forwarded", func(t *testing.T) {
		kubeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "application/json", r.Header.Get("Accept"))
			assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
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
				{Key: "Accept", Value: "application/json"},
				{Key: "Content-Type", Value: "application/json"},
			},
		})

		assert.Equal(t, int32(http.StatusOK), resp.statusCode)
		assert.ElementsMatch(t, []string{"r1", "r2"}, resp.headers["X-Response"])
	})

	t.Run("sensitive headers are stripped", func(t *testing.T) {
		kubeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Empty(t, r.Header.Get("Authorization"))
			assert.Empty(t, r.Header.Get("Impersonate-User"))
			assert.Empty(t, r.Header.Get("Impersonate-Group"))
			assert.Equal(t, "application/json", r.Header.Get("Accept"))
			w.WriteHeader(http.StatusOK)
		}))
		defer kubeServer.Close()

		resp := proxyViaTestServer(t, kubeServer, &pb.HttpRequest{
			RequestId: "req-stripped",
			Method:    http.MethodGet,
			Path:      "/test",
			Headers: []*pb.Header{
				{Key: "Authorization", Value: "Bearer stolen-token"},
				{Key: "Impersonate-User", Value: "cluster-admin"},
				{Key: "Impersonate-Group", Value: "system:masters"},
				{Key: "Accept", Value: "application/json"},
			},
		})

		assert.Equal(t, int32(http.StatusOK), resp.statusCode)
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

	t.Run("blocked subresources rejected with 403", func(t *testing.T) {
		kubeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("should not reach kube server")
		}))
		defer kubeServer.Close()

		for _, sub := range []string{"exec", "attach", "portforward", "proxy", "token", "ephemeralcontainers"} {
			resp := proxyViaTestServer(t, kubeServer, &pb.HttpRequest{
				RequestId: "req-" + sub,
				Method:    http.MethodGet,
				Path:      "/api/v1/namespaces/default/pods/foo/" + sub,
			})

			assert.Equal(t, int32(http.StatusForbidden), resp.statusCode, "subresource %s should be blocked", sub)
			assert.Contains(t, string(resp.body), "not allowed", "subresource %s should be blocked", sub)
		}
	})

	t.Run("blocked subresources on cluster-scoped resources", func(t *testing.T) {
		kubeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("should not reach kube server")
		}))
		defer kubeServer.Close()

		resp := proxyViaTestServer(t, kubeServer, &pb.HttpRequest{
			RequestId: "req-node-proxy",
			Method:    http.MethodGet,
			Path:      "/api/v1/nodes/my-node/proxy",
		})

		assert.Equal(t, int32(http.StatusForbidden), resp.statusCode)
	})

	t.Run("blocked subresources on named API groups", func(t *testing.T) {
		kubeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("should not reach kube server")
		}))
		defer kubeServer.Close()

		resp := proxyViaTestServer(t, kubeServer, &pb.HttpRequest{
			RequestId: "req-apis-exec",
			Method:    http.MethodGet,
			Path:      "/apis/apps/v1/namespaces/default/deployments/foo/exec",
		})

		assert.Equal(t, int32(http.StatusForbidden), resp.statusCode)
	})

	t.Run("URL-encoded subresource rejected with 403", func(t *testing.T) {
		kubeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("should not reach kube server")
		}))
		defer kubeServer.Close()

		resp := proxyViaTestServer(t, kubeServer, &pb.HttpRequest{
			RequestId: "req-encoded-exec",
			Method:    http.MethodGet,
			Path:      "/api/v1/namespaces/default/pods/foo/%65xec",
		})

		assert.Equal(t, int32(http.StatusForbidden), resp.statusCode)
		assert.Contains(t, string(resp.body), "not allowed")
	})

	t.Run("resource named 'exec' is allowed - not a subresource", func(t *testing.T) {
		kubeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"kind":"Pod"}`)
		}))
		defer kubeServer.Close()

		resp := proxyViaTestServer(t, kubeServer, &pb.HttpRequest{
			RequestId: "req-pod-named-exec",
			Method:    http.MethodGet,
			Path:      "/api/v1/namespaces/default/pods/exec",
		})

		assert.Equal(t, int32(http.StatusOK), resp.statusCode)
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

	t.Run("regular resource paths are allowed", func(t *testing.T) {
		kubeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer kubeServer.Close()

		for _, path := range []string{
			"/api/v1/namespaces",
			"/api/v1/namespaces/default/pods",
			"/api/v1/namespaces/default/pods/my-pod",
			"/api/v1/namespaces/default/pods/my-pod/log",
			"/api/v1/nodes",
			"/api/v1/nodes/my-node",
			"/apis/apps/v1/namespaces/default/deployments",
		} {
			resp := proxyViaTestServer(t, kubeServer, &pb.HttpRequest{
				RequestId: "req-ok",
				Method:    http.MethodGet,
				Path:      path,
			})

			assert.Equal(t, int32(http.StatusOK), resp.statusCode, "path %s should be allowed", path)
		}
	})
}

func TestExtractSubresource(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{"namespaced subresource", "/api/v1/namespaces/default/pods/foo/exec", "exec"},
		{"namespaced get - no subresource", "/api/v1/namespaces/default/pods/exec", ""},
		{"namespaced list - no subresource", "/api/v1/namespaces/default/pods", ""},
		{"cluster-scoped subresource", "/api/v1/nodes/my-node/proxy", "proxy"},
		{"cluster-scoped get - no subresource", "/api/v1/nodes/my-node", ""},
		{"named API group subresource", "/apis/apps/v1/namespaces/default/deployments/foo/status", "status"},
		{"named API group get - no subresource", "/apis/apps/v1/namespaces/default/deployments/foo", ""},
		{"non-API path - no subresource", "/healthz", ""},
		{"root path - no subresource", "/", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, extractSubresource(tt.path))
		})
	}
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
