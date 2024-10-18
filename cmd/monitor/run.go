package monitor

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes"

	"github.com/castai/cluster-controller/cmd/utils"
	"github.com/castai/cluster-controller/internal/castai"
	"github.com/castai/cluster-controller/internal/config"
	"github.com/castai/cluster-controller/internal/controller/logexporter"
	"github.com/castai/cluster-controller/internal/monitor"
)

const (
	maxRequestTimeout = 15 * time.Second
)

func run(ctx context.Context) error {
	cfg := config.Get()
	if cfg.API.Key == "" {
		return errors.New("env variable \"API_KEY\" is required")
	}
	if cfg.API.URL == "" {
		return errors.New("env variable \"API_URL\" is required")
	}
	binVersion := ctx.Value(utils.ClusterControllerVersionKey).(*config.ClusterControllerVersion)

	logger := logexporter.NewLogger(cfg.Log.Level)
	log := logger.WithFields(logrus.Fields{
		"cluster_id": cfg.ClusterID,
		"version":    binVersion.String(),
	})

	cl, err := castai.NewRestyClient(cfg.API.URL, cfg.API.Key, cfg.TLS.CACert, logger.Level, binVersion, maxRequestTimeout)
	if err != nil {
		log.Fatalf("failed to create castai client: %v", err)
	}
	client := castai.NewClient(logger, cl, cfg.ClusterID)

	logexporter.SetupLogExporter(logger, client)

	clusterIDHandler := func(clusterID string) {
		log.Data["cluster_id"] = clusterID
		log.Data["version"] = binVersion.Version
	}

	return runMonitorMode(ctx, log, &cfg, clusterIDHandler)
}

func runMonitorMode(ctx context.Context, log *logrus.Entry, cfg *config.Config, clusterIDChanged func(clusterID string)) error {
	restconfig, err := config.RetrieveKubeConfig(log)
	if err != nil {
		return fmt.Errorf("retrieving kubeconfig: %w", err)
	}
	clientset, err := kubernetes.NewForConfig(restconfig)
	if err != nil {
		return fmt.Errorf("obtaining kubernetes clientset: %w", err)
	}

	return monitor.Run(ctx, log, clientset, cfg.MonitorMetadata, cfg.SelfPod, clusterIDChanged)
}
