package main

import (
	"context"
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

const (
	defaultHealthPort        = 8091
	defaultHeartbeatInterval = 30 * time.Second
)

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

	apiURL := os.Getenv("API_URL")
	if apiURL == "" {
		return fmt.Errorf("API_URL is required")
	}
	clusterID := os.Getenv("CLUSTER_ID")
	if clusterID == "" {
		return fmt.Errorf("CLUSTER_ID is required")
	}

	tunnelAddr := os.Getenv("TUNNEL_ADDRESS")
	if tunnelAddr == "" {
		addr, err := deriveTunnelAddress(apiURL)
		if err != nil {
			return fmt.Errorf("deriving tunnel address from API_URL: %w", err)
		}
		tunnelAddr = addr
	}

	heartbeatInterval := defaultHeartbeatInterval
	if v := os.Getenv("TUNNEL_HEARTBEAT_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("invalid TUNNEL_HEARTBEAT_INTERVAL: %w", err)
		}
		heartbeatInterval = d
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
		Address:           tunnelAddr,
		ClusterID:         clusterID,
		TLSCACert:         os.Getenv("TLS_CA_CERT"),
		HeartbeatInterval: heartbeatInterval,
	}

	client, err := tunnel.NewClient(log, cfg, restCfg)
	if err != nil {
		return fmt.Errorf("creating tunnel client: %w", err)
	}

	healthAddr := fmt.Sprintf(":%d", healthPort)
	healthSrv := &http.Server{
		Addr:              healthAddr,
		ReadHeaderTimeout: 5 * time.Second,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if client.IsConnected() {
				w.WriteHeader(http.StatusOK)
			} else {
				w.WriteHeader(http.StatusServiceUnavailable)
			}
		}),
	}

	go func() {
		log.Infof("health server listening on %s", healthAddr)
		if err := healthSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.WithError(err).Error("health server failed")
		}
	}()

	go func() {
		<-ctx.Done()
		healthSrv.Close()
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
