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
	"sync/atomic"
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
	t.Run("GET round-trip streams status, body chunks, and end", func(t *testing.T) {
		kubeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "/api/v1/namespaces", r.URL.Path)
			assert.Equal(t, http.MethodGet, r.Method)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"kind":"NamespaceList"}`)
		}))
		defer kubeServer.Close()

		msgs := proxyViaTestStream(t, kubeServer, &pb.HttpRequest{
			RequestId: "req-1",
			Method:    http.MethodGet,
			Path:      "/api/v1/namespaces",
		})

		start := findStart(msgs, "req-1")
		require.NotNil(t, start)
		assert.Equal(t, int32(http.StatusOK), start.StatusCode)

		body := collectBody(msgs, "req-1")
		assert.Contains(t, string(body), "NamespaceList")

		assert.True(t, hasEnd(msgs, "req-1"))
	})

	t.Run("POST with body round-trip", func(t *testing.T) {
		kubeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			w.WriteHeader(http.StatusCreated)
			w.Write(body)
		}))
		defer kubeServer.Close()

		msgs := proxyViaTestStream(t, kubeServer, &pb.HttpRequest{
			RequestId: "req-2",
			Method:    http.MethodPost,
			Path:      "/api/v1/namespaces",
			Body:      []byte(`{"metadata":{"name":"test"}}`),
			Headers: []*pb.Header{
				{Key: "Content-Type", Value: "application/json"},
			},
		})

		start := findStart(msgs, "req-2")
		require.NotNil(t, start)
		assert.Equal(t, int32(http.StatusCreated), start.StatusCode)

		body := collectBody(msgs, "req-2")
		assert.JSONEq(t, `{"metadata":{"name":"test"}}`, string(body))
		assert.True(t, hasEnd(msgs, "req-2"))
	})

	t.Run("multi-value headers are preserved", func(t *testing.T) {
		kubeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, []string{"val1", "val2"}, r.Header.Values("X-Custom"))
			w.Header().Add("X-Response", "r1")
			w.Header().Add("X-Response", "r2")
			w.WriteHeader(http.StatusOK)
		}))
		defer kubeServer.Close()

		msgs := proxyViaTestStream(t, kubeServer, &pb.HttpRequest{
			RequestId: "req-3",
			Method:    http.MethodGet,
			Path:      "/test",
			Headers: []*pb.Header{
				{Key: "X-Custom", Value: "val1"},
				{Key: "X-Custom", Value: "val2"},
			},
		})

		start := findStart(msgs, "req-3")
		require.NotNil(t, start)

		var responseValues []string
		for _, h := range start.Headers {
			if h.Key == "X-Response" {
				responseValues = append(responseValues, h.Value)
			}
		}
		assert.ElementsMatch(t, []string{"r1", "r2"}, responseValues)
	})

	t.Run("kube server error sends error through stream", func(t *testing.T) {
		srv, addr := startTestServer(t, func(stream grpc.BidiStreamingServer[pb.TunnelMessage, pb.TunnelMessage]) error {
			stream.Send(&pb.TunnelMessage{
				Payload: &pb.TunnelMessage_HttpRequest{
					HttpRequest: &pb.HttpRequest{
						RequestId: "req-4",
						Method:    http.MethodGet,
						Path:      "/api/v1/pods",
					},
				},
			})

			var msgs []*pb.TunnelMessage
			for {
				msg, err := stream.Recv()
				if err != nil {
					return err
				}
				msgs = append(msgs, msg)
				if msg.GetHttpResponseEnd() != nil {
					break
				}
			}

			start := findStart(msgs, "req-4")
			if start == nil || start.StatusCode != http.StatusBadGateway {
				return fmt.Errorf("expected 502, got %v", start)
			}
			return nil
		})
		defer srv.GracefulStop()

		c := &Client{
			log:        logrus.New(),
			cfg:        Config{HeartbeatInterval: time.Hour},
			httpClient: &http.Client{Transport: &http.Transport{}},
			kubeURL:    "http://127.0.0.1:1",
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		require.NoError(t, err)
		defer conn.Close()

		client := pb.NewClusterTunnelClient(conn)
		stream, err := client.Connect(ctx)
		require.NoError(t, err)

		_ = c.handleStream(ctx, stream)
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

		msgs := proxyViaTestStream(t, kubeServer, &pb.HttpRequest{
			RequestId: "req-stream",
			Method:    http.MethodGet,
			Path:      "/logs",
		})

		start := findStart(msgs, "req-stream")
		require.NotNil(t, start)
		assert.Equal(t, int32(http.StatusOK), start.StatusCode)

		body := collectBody(msgs, "req-stream")
		assert.Contains(t, string(body), "chunk-0")
		assert.Contains(t, string(body), "chunk-1")
		assert.Contains(t, string(body), "chunk-2")
		assert.True(t, hasEnd(msgs, "req-stream"))
	})
}

func TestStreamHandling(t *testing.T) {
	t.Run("concurrent requests with request_id correlation", func(t *testing.T) {
		kubeServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintf(w, "path=%s", r.URL.Path)
		}))
		defer kubeServer.Close()

		srv, addr := startTestServer(t, func(stream grpc.BidiStreamingServer[pb.TunnelMessage, pb.TunnelMessage]) error {
			const numRequests = 10
			for i := range numRequests {
				if err := stream.Send(&pb.TunnelMessage{
					Payload: &pb.TunnelMessage_HttpRequest{
						HttpRequest: &pb.HttpRequest{
							RequestId: fmt.Sprintf("req-%d", i),
							Method:    http.MethodGet,
							Path:      fmt.Sprintf("/api/v1/ns/%d", i),
						},
					},
				}); err != nil {
					return err
				}
			}

			bodies := make(map[string]*bytes.Buffer)
			ended := make(map[string]bool)

			for len(ended) < numRequests {
				msg, err := stream.Recv()
				if err != nil {
					return err
				}
				if s := msg.GetHttpResponseStart(); s != nil {
					bodies[s.RequestId] = &bytes.Buffer{}
				}
				if b := msg.GetHttpResponseBody(); b != nil {
					if buf, ok := bodies[b.RequestId]; ok {
						buf.Write(b.Data)
					}
				}
				if e := msg.GetHttpResponseEnd(); e != nil {
					ended[e.RequestId] = true
				}
			}

			for i := range numRequests {
				id := fmt.Sprintf("req-%d", i)
				expected := fmt.Sprintf("path=/api/v1/ns/%d", i)
				got := bodies[id].String()
				if got != expected {
					return fmt.Errorf("request %s: got %q, want %q", id, got, expected)
				}
			}

			return nil
		})
		defer srv.GracefulStop()

		c := &Client{
			log:        logrus.New(),
			cfg:        Config{HeartbeatInterval: time.Hour},
			httpClient: kubeServer.Client(),
			kubeURL:    kubeServer.URL,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		require.NoError(t, err)
		defer conn.Close()

		client := pb.NewClusterTunnelClient(conn)
		stream, err := client.Connect(ctx)
		require.NoError(t, err)

		streamErr := make(chan error, 1)
		go func() {
			streamErr <- c.handleStream(ctx, stream)
		}()

		select {
		case err := <-streamErr:
			if err != nil && ctx.Err() == nil {
				require.NoError(t, err)
			}
		case <-ctx.Done():
		}
	})

	t.Run("heartbeat is sent", func(t *testing.T) {
		var heartbeatReceived atomic.Bool

		srv, addr := startTestServer(t, func(stream grpc.BidiStreamingServer[pb.TunnelMessage, pb.TunnelMessage]) error {
			for {
				msg, err := stream.Recv()
				if err != nil {
					return err
				}
				if msg.GetHeartbeat() != nil {
					heartbeatReceived.Store(true)
					return nil
				}
			}
		})
		defer srv.GracefulStop()

		c := &Client{
			log: logrus.New(),
			cfg: Config{
				HeartbeatInterval: 50 * time.Millisecond,
			},
			httpClient: http.DefaultClient,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		require.NoError(t, err)
		defer conn.Close()

		client := pb.NewClusterTunnelClient(conn)
		stream, err := client.Connect(ctx)
		require.NoError(t, err)

		_ = c.handleStream(ctx, stream)
		assert.True(t, heartbeatReceived.Load())
	})

	t.Run("metadata is sent with connect", func(t *testing.T) {
		var gotClusterID string

		srv, addr := startTestServer(t, func(stream grpc.BidiStreamingServer[pb.TunnelMessage, pb.TunnelMessage]) error {
			md, ok := metadata.FromIncomingContext(stream.Context())
			if ok {
				if vals := md.Get(metadataKeyClusterID); len(vals) > 0 {
					gotClusterID = vals[0]
				}
			}
			<-stream.Context().Done()
			return nil
		})
		defer srv.GracefulStop()

		c := &Client{
			log: logrus.New(),
			cfg: Config{
				Address:           addr,
				ClusterID:         "test-cluster-123",
				HeartbeatInterval: time.Hour,
			},
			httpClient: http.DefaultClient,
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()

		_ = c.connect(ctx)

		assert.Equal(t, "test-cluster-123", gotClusterID)
	})
}

// Test helpers

func proxyViaTestStream(t *testing.T, kubeServer *httptest.Server, req *pb.HttpRequest) []*pb.TunnelMessage {
	t.Helper()

	var result []*pb.TunnelMessage
	var mu sync.Mutex

	srv, addr := startTestServer(t, func(stream grpc.BidiStreamingServer[pb.TunnelMessage, pb.TunnelMessage]) error {
		if err := stream.Send(&pb.TunnelMessage{
			Payload: &pb.TunnelMessage_HttpRequest{HttpRequest: req},
		}); err != nil {
			return err
		}

		for {
			msg, err := stream.Recv()
			if err != nil {
				return err
			}
			mu.Lock()
			result = append(result, msg)
			mu.Unlock()
			if msg.GetHttpResponseEnd() != nil {
				return nil
			}
		}
	})
	defer srv.GracefulStop()

	c := &Client{
		log:        logrus.New(),
		cfg:        Config{HeartbeatInterval: time.Hour},
		httpClient: kubeServer.Client(),
		kubeURL:    kubeServer.URL,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer conn.Close()

	client := pb.NewClusterTunnelClient(conn)
	stream, err := client.Connect(ctx)
	require.NoError(t, err)

	_ = c.handleStream(ctx, stream)

	mu.Lock()
	defer mu.Unlock()
	return result
}

func findStart(msgs []*pb.TunnelMessage, requestID string) *pb.HttpResponseStart {
	for _, m := range msgs {
		if s := m.GetHttpResponseStart(); s != nil && s.RequestId == requestID {
			return s
		}
	}
	return nil
}

func collectBody(msgs []*pb.TunnelMessage, requestID string) []byte {
	var buf bytes.Buffer
	for _, m := range msgs {
		if b := m.GetHttpResponseBody(); b != nil && b.RequestId == requestID {
			buf.Write(b.Data)
		}
	}
	return buf.Bytes()
}

func hasEnd(msgs []*pb.TunnelMessage, requestID string) bool {
	for _, m := range msgs {
		if e := m.GetHttpResponseEnd(); e != nil && e.RequestId == requestID {
			return true
		}
	}
	return false
}

type testServer struct {
	pb.UnimplementedClusterTunnelServer
	handler func(stream grpc.BidiStreamingServer[pb.TunnelMessage, pb.TunnelMessage]) error
}

func (s *testServer) Connect(stream grpc.BidiStreamingServer[pb.TunnelMessage, pb.TunnelMessage]) error {
	return s.handler(stream)
}

func startTestServer(t *testing.T, handler func(stream grpc.BidiStreamingServer[pb.TunnelMessage, pb.TunnelMessage]) error) (*grpc.Server, string) {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	srv := grpc.NewServer()
	pb.RegisterClusterTunnelServer(srv, &testServer{handler: handler})

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
