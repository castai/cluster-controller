package testserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes"

	"github.com/castai/cluster-controller/internal/config"
	"github.com/castai/cluster-controller/loadtest"
	"github.com/castai/cluster-controller/loadtest/scenarios"
)

func run(ctx context.Context) error {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	// TODO: Export as envVars
	cfg := loadtest.Config{
		Port: 8080,
	}
	logger.Info("creating test server")
	// TODO: Defaults...
	testServer := loadtest.NewTestServer(logger, loadtest.TestServerConfig{
		BufferSize:               1000,
		MaxActionsPerCall:        500,
		TimeoutWaitingForActions: 60 * time.Second,
	})

	// Not ideal but fast
	discardLogger := logrus.New()
	discardLogger.Out = io.Discard
	restConfig, err := config.RetrieveKubeConfig(discardLogger)
	if err != nil {
		return fmt.Errorf("failed to get kubeconfig: %w", err)
	}

	clientSet, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("obtaining kubernetes clientset: %w", err)
	}

	go func() {
		logger.Info("Starting HTTP server for test")
		err = loadtest.NewHttpServer(ctx, cfg, testServer)
		if err != nil {
			logger.Error("", "err", err)
			panic(err)
		}
	}()

	ch := testServer.GetActionsPushChannel()

	testScenarios := []scenarios.TestScenario{
		scenarios.PodEvents(2000, logger),
		scenarios.StuckDrain(100, 1, logger),
	}

	var wg sync.WaitGroup
	wg.Add(len(testScenarios))
	errs := make(chan error, len(testScenarios))

	for i, test := range testScenarios {
		go func() {
			defer wg.Done()
			logger.Info(fmt.Sprintf("Starting test scenario %d", i))

			err := scenarios.RunScenario(ctx, test, ch, logger, clientSet)
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

	// TODO: Wait for server channel to be empty as well
	return errors.Join(receivedErrors...)
}
