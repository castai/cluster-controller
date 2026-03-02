package kubectl

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestServer(opts ...func(*Server)) *Server {
	srv := NewServer(logrus.New(), Config{
		AllowedCommands: []string{"get", "logs", "describe", "events", "top"},
		CommandTimeout:  5 * time.Second,
	})
	for _, o := range opts {
		o(srv)
	}
	return srv
}

func postKubectl(handler http.Handler, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/kubectl", bytes.NewBufferString(body))
	handler.ServeHTTP(rec, req)
	return rec
}

func TestHandleKubectl_Validation(t *testing.T) {
	srv := newTestServer()
	h := srv.Handler()

	t.Run("invalid JSON returns 400", func(t *testing.T) {
		rec := postKubectl(h, `not json`)
		assert.Equal(t, http.StatusBadRequest, rec.Code)

		var resp errorResponse
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
		assert.Contains(t, resp.Error, "invalid request body")
	})

	t.Run("empty args returns 400", func(t *testing.T) {
		rec := postKubectl(h, `{"args": []}`)
		assert.Equal(t, http.StatusBadRequest, rec.Code)

		var resp errorResponse
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
		assert.Contains(t, resp.Error, "args must not be empty")
	})

	t.Run("missing args field returns 400", func(t *testing.T) {
		rec := postKubectl(h, `{}`)
		assert.Equal(t, http.StatusBadRequest, rec.Code)

		var resp errorResponse
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
		assert.Contains(t, resp.Error, "args must not be empty")
	})

	t.Run("disallowed subcommand returns 400", func(t *testing.T) {
		rec := postKubectl(h, `{"args": ["delete", "pod", "my-pod"]}`)
		assert.Equal(t, http.StatusBadRequest, rec.Code)

		var resp errorResponse
		require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
		assert.Contains(t, resp.Error, "disallowed subcommand: delete")
	})

	t.Run("disallowed subcommand apply returns 400", func(t *testing.T) {
		rec := postKubectl(h, `{"args": ["apply", "-f", "file.yaml"]}`)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("wrong HTTP method returns 405", func(t *testing.T) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/kubectl", nil)
		h.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	})
}

func TestHandleKubectl_AllowedCommands(t *testing.T) {
	for _, cmd := range []string{"get", "logs", "describe", "events", "top"} {
		t.Run(cmd+" is allowed", func(t *testing.T) {
			srv := newTestServer(func(s *Server) {
				s.kubectlBin = echoCmd()
			})
			body := `{"args": ["` + cmd + `", "pods"]}`
			rec := postKubectl(srv.Handler(), body)
			assert.Equal(t, http.StatusOK, rec.Code)

			var resp response
			require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
			assert.Equal(t, 0, resp.ExitCode)
		})
	}
}

func TestHandleKubectl_NonZeroExit(t *testing.T) {
	srv := newTestServer(func(s *Server) {
		s.kubectlBin = falseCmd()
	})
	rec := postKubectl(srv.Handler(), `{"args": ["get", "pods"]}`)
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp response
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.NotEqual(t, 0, resp.ExitCode)
}

func TestHandleKubectl_Timeout(t *testing.T) {
	srv := newTestServer(func(s *Server) {
		s.kubectlBin = sleepCmd()
		s.commandTimeout = 50 * time.Millisecond
	})
	rec := postKubectl(srv.Handler(), `{"args": ["get", "10"]}`)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	var resp errorResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Contains(t, resp.Error, "timed out")
}

func TestHandleKubectl_BinaryNotFound(t *testing.T) {
	srv := newTestServer(func(s *Server) {
		s.kubectlBin = "/nonexistent/binary"
	})
	rec := postKubectl(srv.Handler(), `{"args": ["get", "pods"]}`)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	var resp errorResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Contains(t, resp.Error, "failed to execute command")
}

func echoCmd() string {
	if runtime.GOOS == "windows" {
		return "cmd /c echo"
	}
	return "echo"
}

func falseCmd() string {
	if runtime.GOOS == "windows" {
		return "cmd /c exit 1"
	}
	return "false"
}

func sleepCmd() string {
	if runtime.GOOS == "windows" {
		return "timeout"
	}
	return "sleep"
}
