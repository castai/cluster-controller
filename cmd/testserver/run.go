package testserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/castai/cluster-controller/loadtest"
	"github.com/castai/cluster-controller/loadtest/scenarios"
)

func run(ctx context.Context) error {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg := loadtest.GetConfig()
	logger.Info("creating test server")

	testServer := loadtest.NewTestServer(logger, loadtest.TestServerConfig{
		MaxActionsPerCall:        1000,
		TimeoutWaitingForActions: 60 * time.Second,
	})

	clientSet, err := createK8SClient(cfg, logger)
	if err != nil {
		return err
	}

	go func() {
		logger.Info("Starting HTTP server for test")
		err = loadtest.NewHttpServer(ctx, cfg, testServer)
		if err != nil {
			logger.Error("", "err", err)
			panic(err)
		}
	}()

	testScenarios := []scenarios.TestScenario{
		//scenarios.PodEvents(5000, logger),
		//scenarios.StuckDrain(100, 60, logger),
		scenarios.StuckDrain(10, 1, logger),
	}

	var wg sync.WaitGroup
	wg.Add(len(testScenarios))
	errs := make(chan error, len(testScenarios))

	for i, test := range testScenarios {
		go func() {
			defer wg.Done()
			logger.Info(fmt.Sprintf("Starting test scenario %d", i))

			err := scenarios.RunScenario(ctx, test, testServer, logger, clientSet)
			errs <- err
		}()
	}

	logger.Info("Waiting for test scenarios to finish")
	wg.Wait()

	close(errs)
	receivedErrors := make([]error, 0)
	for err := range errs {
		if err != nil {
			receivedErrors = append(receivedErrors, err)
		}
	}
	logger.Info(fmt.Sprintf("All test scenarios are done, received (%d) errors, exiting", len(receivedErrors)))

	return errors.Join(receivedErrors...)
}

func createK8SClient(cfg loadtest.Config, logger *slog.Logger) (*kubernetes.Clientset, error) {
	if cfg.KubeConfig == "" {
		logger.Info("Using in-cluster configuration")
		restConfig, err := rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("error creating in-cluster config: %w", err)
		}
		clientSet, err := kubernetes.NewForConfig(restConfig)
		if err != nil {
			return nil, fmt.Errorf("obtaining kubernetes clientset: %w", err)
		}
		return clientSet, nil
	}

	logger.Info(fmt.Sprintf("Using kubeconfig from %q", cfg.KubeConfig))
	data, err := os.ReadFile(cfg.KubeConfig)
	if err != nil {
		return nil, fmt.Errorf("reading kubeconfig at %s: %w", cfg.KubeConfig, err)
	}

	restConfig, err := clientcmd.RESTConfigFromKubeConfig(data)
	if err != nil {
		return nil, fmt.Errorf("creating rest config from %q: %w", cfg.KubeConfig, err)
	}

	clientSet, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("obtaining kubernetes clientset: %w", err)
	}
	return clientSet, nil
}
