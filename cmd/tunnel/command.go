package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/castai/cluster-controller/internal/tunnel"
)

const defaultHealthPort = 8091

func newCommand() *cobra.Command {
	return &cobra.Command{
		Use: "castai-tunnel",
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd.Context())
		},
	}
}

func run(ctx context.Context) error {
	log := logrus.New()
	if v := os.Getenv("LOG_LEVEL"); v != "" {
		lvl, err := logrus.ParseLevel(v)
		if err != nil {
			return fmt.Errorf("invalid LOG_LEVEL: %w", err)
		}
		log.SetLevel(lvl)
	}

	log.Infof("starting castai-tunnel version %s", Version)

	apiURL := os.Getenv("API_URL")
	if apiURL == "" {
		return fmt.Errorf("API_URL is required")
	}
	clusterID := os.Getenv("CLUSTER_ID")
	if clusterID == "" {
		return fmt.Errorf("CLUSTER_ID is required")
	}

	apiKey := os.Getenv("API_KEY")
	if apiKey == "" {
		return fmt.Errorf("API_KEY is required")
	}

	tunnelAddr := os.Getenv("TUNNEL_ADDRESS")
	if tunnelAddr == "" {
		addr, err := deriveTunnelAddress(apiURL)
		if err != nil {
			return fmt.Errorf("deriving tunnel address from API_URL: %w", err)
		}
		tunnelAddr = addr
	}

	healthPort := defaultHealthPort
	if v := os.Getenv("TUNNEL_HEALTH_PORT"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("invalid TUNNEL_HEALTH_PORT: %w", err)
		}
		healthPort = p
	}

	restCfg, err := loadKubeConfig(log)
	if err != nil {
		return fmt.Errorf("loading kube config: %w", err)
	}

	cfg := tunnel.Config{
		Address:   tunnelAddr,
		ClusterID: clusterID,
		APIKey:    apiKey,
		TLSCACert: os.Getenv("TLS_CA_CERT"),
	}

	client, err := tunnel.NewClient(log, cfg, restCfg)
	if err != nil {
		return fmt.Errorf("creating tunnel client: %w", err)
	}

	healthAddr := fmt.Sprintf(":%d", healthPort)
	healthMux := http.NewServeMux()
	healthMux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		if client.IsConnected() {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	})
	healthSrv := &http.Server{
		Addr:              healthAddr,
		ReadHeaderTimeout: 5 * time.Second,
		Handler:           healthMux,
	}

	go func() {
		log.Infof("health server listening on %s", healthAddr)
		if err := healthSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.WithError(err).Error("health server failed")
		}
	}()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := healthSrv.Shutdown(shutdownCtx); err != nil {
			log.WithError(err).Error("health server shutdown")
		}
	}()

	log.WithField("address", tunnelAddr).Info("starting tunnel client")
	return client.Run(ctx)
}

func loadKubeConfig(log logrus.FieldLogger) (*rest.Config, error) {
	if kubeconfig := os.Getenv("KUBECONFIG"); kubeconfig != "" {
		log.Debug("using kubeconfig from KUBECONFIG env")
		return clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	log.Debug("using in-cluster kubeconfig")
	return rest.InClusterConfig()
}

func deriveTunnelAddress(apiURL string) (string, error) {
	u, err := url.Parse(apiURL)
	if err != nil {
		return "", fmt.Errorf("parsing API_URL: %w", err)
	}

	host := u.Hostname()

	// api--user.local.cast.ai → api-grpc--user.local.cast.ai (Tilt dev)
	// api.cast.ai → api-grpc.cast.ai (production)
	if strings.HasPrefix(host, "api--") {
		host = "api-grpc--" + strings.TrimPrefix(host, "api--")
	} else if strings.HasPrefix(host, "api.") {
		host = "api-grpc." + strings.TrimPrefix(host, "api.")
	} else {
		return "", fmt.Errorf("cannot derive gRPC host from %q, set TUNNEL_ADDRESS explicitly", host)
	}

	return host + ":443", nil
}
