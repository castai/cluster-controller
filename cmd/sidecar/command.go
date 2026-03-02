package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"github.com/castai/cluster-controller/internal/kubectl"
)

const (
	defaultPort    = 7070
	defaultTimeout = 30 * time.Second
)

var defaultAllowedCommands = []string{"get", "logs", "describe", "events", "top"}

func newCommand() *cobra.Command {
	return &cobra.Command{
		Use: "castai-sidecar",
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd.Context())
		},
	}
}

func run(ctx context.Context) error {
	log := logrus.New()

	port := defaultPort
	if v := os.Getenv("KUBECTL_PORT"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("invalid KUBECTL_PORT: %w", err)
		}
		port = p
	}

	timeout := defaultTimeout
	if v := os.Getenv("KUBECTL_TIMEOUT"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("invalid KUBECTL_TIMEOUT: %w", err)
		}
		timeout = d
	}

	allowedCommands := defaultAllowedCommands
	if v := os.Getenv("KUBECTL_ALLOWED_COMMANDS"); v != "" {
		allowedCommands = strings.Split(v, ",")
	}

	srv := kubectl.NewServer(log, kubectl.Config{
		AllowedCommands: allowedCommands,
		CommandTimeout:  timeout,
	})

	addr := fmt.Sprintf("127.0.0.1:%d", port)
	log.Infof("starting kubectl sidecar on %s", addr)

	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()
		log.Info("shutting down")
		httpSrv.Close()
	}()

	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("http server: %w", err)
	}
	return nil
}
