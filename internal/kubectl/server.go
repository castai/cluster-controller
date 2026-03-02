package kubectl

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os/exec"
	"time"

	"github.com/sirupsen/logrus"
)

type Server struct {
	log             logrus.FieldLogger
	allowedCommands map[string]struct{}
	commandTimeout  time.Duration
	kubectlBin      string
}

type Config struct {
	AllowedCommands []string
	CommandTimeout  time.Duration
}

func NewServer(log logrus.FieldLogger, cfg Config) *Server {
	allowed := make(map[string]struct{}, len(cfg.AllowedCommands))
	for _, cmd := range cfg.AllowedCommands {
		allowed[cmd] = struct{}{}
	}
	return &Server{
		log:             log,
		allowedCommands: allowed,
		commandTimeout:  cfg.CommandTimeout,
		kubectlBin:      "kubectl",
	}
}

type request struct {
	Args []string `json:"args"`
}

type response struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exitCode"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /kubectl", s.handleKubectl)
	return mux
}

func (s *Server) handleKubectl(w http.ResponseWriter, r *http.Request) {
	var req request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}

	if len(req.Args) == 0 {
		writeError(w, http.StatusBadRequest, "args must not be empty")
		return
	}

	subcommand := req.Args[0]
	if _, ok := s.allowedCommands[subcommand]; !ok {
		writeError(w, http.StatusBadRequest, "disallowed subcommand: "+subcommand)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.commandTimeout)
	defer cancel()

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, s.kubectlBin, req.Args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	exitCode := 0
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			writeError(w, http.StatusInternalServerError, "command timed out")
			return
		}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			s.log.WithError(err).Error("failed to execute kubectl")
			writeError(w, http.StatusInternalServerError, "failed to execute command: "+err.Error())
			return
		}
	}

	s.log.WithFields(logrus.Fields{
		"subcommand": subcommand,
		"exitCode":   exitCode,
	}).Info("kubectl command executed")

	writeJSON(w, http.StatusOK, response{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg})
}
