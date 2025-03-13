package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"

	"github.com/castai/cluster-controller/loadtest"
	"github.com/castai/cluster-controller/loadtest/scenarios"
)

// TODO: Move to corba

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
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

	var kubeconfig string
	if home := homedir.HomeDir(); home != "" {
		kubeconfig = filepath.Join(home, ".kube", "config")
	}

	// Build the Kubernetes configuration from the kubeconfig file.
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		panic(fmt.Errorf("failed to build config: %v", err))
	}

	// Create a clientset based on the configuration.
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(fmt.Errorf("failed to create clientset: %v", err))
	}

	namespaceForTest := "loadtest-1"
	ctx := context.Background()

	// TODO: Extract
	logger.Info(fmt.Sprintf("Deleting namespace %v", namespaceForTest))
	err = clientset.CoreV1().Namespaces().Delete(ctx, namespaceForTest, metav1.DeleteOptions{
		GracePeriodSeconds: lo.ToPtr(int64(0)),
		PropagationPolicy:  lo.ToPtr(metav1.DeletePropagationBackground),
	})
	if err != nil && !apierrors.IsNotFound(err) {
		panic(err)
	}

	for range 300 {
		_, err = clientset.CoreV1().Namespaces().Get(ctx, namespaceForTest, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			break
		}
		time.Sleep(1 * time.Second)
	}

	logger.Info(fmt.Sprintf("Recreating namespace %v", namespaceForTest))
	_, err = clientset.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespaceForTest,
		},
	}, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		panic(err)
	}
	// end

	go func() {
		ctx := context.Background()

		ch := testServer.GetActionsPushChannel()

		_, _, eventsScenario := scenarios.PodEvents(2000, logger)()

		prepareDrain, cleanupDrain, drainScenario := scenarios.StuckDrain(100, 1, logger)()

		err := prepareDrain(ctx, namespaceForTest, clientset)
		if err != nil {
			panic(err)
		}

		go func() {
			err := eventsScenario(ctx, ch)
			if err != nil {
				logger.Error(fmt.Sprintf("failed events scenario with %v", err))
			}
		}()

		go func() {
			err := drainScenario(ctx, ch)
			if err != nil {
				logger.Error(fmt.Sprintf("failed to drain with %v", err))
			}

			err = cleanupDrain(ctx, namespaceForTest, clientset)
			if err != nil {
				logger.Error(fmt.Sprintf("failed to cleanup with %v", err))
			}
		}()
	}()

	logger.Info(fmt.Sprintf("starting http server on port %d", cfg.Port))
	// TODO: Cleanup
	err = loadtest.NewHttpServer(cfg, testServer)
	if err != nil {
		panic(err)
	}
}
