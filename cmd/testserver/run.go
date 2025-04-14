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
	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/flowcontrol"

	"github.com/castai/cluster-controller/internal/helm"
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

	clientSet, dynamicClient, apiExtClient, helmClient, err := createK8SClients(cfg, logger)
	if err != nil {
		return err
	}
	logger.Info(fmt.Sprintf("Created %d clients", len([]any{clientSet, dynamicClient, apiExtClient, helmClient})))

	go func() {
		logger.Info("Starting HTTP server for test")
		err = loadtest.NewHttpServer(ctx, cfg, testServer)
		if err != nil {
			logger.Error("", "err", err)
			panic(err)
		}
	}()

	// Choose scenarios below by adding/removing/etc. instances of scenarios.XXX()
	// All scenarios in the list run in parallel (but not necessarily at the same time if preparation takes different time).
	testScenarios := []scenarios.TestScenario{
		scenarios.CheckNodeDeletedStuck(300, logger),
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

func createK8SClients(cfg loadtest.Config, logger *slog.Logger) (*kubernetes.Clientset, *dynamic.DynamicClient, *apiextensionsclientset.Clientset, helm.Client, error) {
	rateLimiter := flowcontrol.NewTokenBucketRateLimiter(100, 200)

	var restConfig *rest.Config
	var err error

	switch {
	case cfg.KubeConfig != "":
		logger.Info(fmt.Sprintf("Using kubeconfig from %q", cfg.KubeConfig))
		data, err := os.ReadFile(cfg.KubeConfig)
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("reading kubeconfig at %s: %w", cfg.KubeConfig, err)
		}

		restConfig, err = clientcmd.RESTConfigFromKubeConfig(data)
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("creating rest config from %q: %w", cfg.KubeConfig, err)
		}
	default:
		logger.Info("Using in-cluster configuration")
		restConfig, err = rest.InClusterConfig()
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("error creating in-cluster config: %w", err)
		}
	}

	restConfig.RateLimiter = rateLimiter

	clientSet, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("obtaining kubernetes clientset: %w", err)
	}
	dynamicClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("obtaining dynamic client: %w", err)
	}
	apiextensionsClient, err := apiextensionsclientset.NewForConfig(restConfig)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("obtaining apiextensions client: %w", err)
	}

	discard := logrus.New()
	discard.Out = io.Discard
	helmClient := helm.NewClient(discard, helm.NewChartLoader(discard), restConfig)

	return clientSet, dynamicClient, apiextensionsClient, helmClient, nil
}
