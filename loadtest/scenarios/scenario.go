package scenarios

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"time"

	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/castai/cluster-controller/internal/castai"
)

// TODO Spend more than 2 seconds thinking about names

type ActionExecutor interface {
	// ExecuteActions is expected to execute all actions and wait for ack before returning; otherwise cleanups might run too early.
	ExecuteActions(ctx context.Context, actions []castai.ClusterAction)
}

type TestScenario interface {
	Name() string
	// Preparation should create any necessary resources in the cluster for the test so it runs in realistic env.
	Preparation(ctx context.Context, namespace string, clientset kubernetes.Interface) error
	// Cleanup should delete any items created by the preparation or the test itself.
	// It might be called even if Preparation or Run did not complete so it should handle those cases gracefully.
	// The scenario's namespace is deleted at the end but ideally scenarios delete their resources as well,
	// otherwise namespace deletion can take very long to propagate.
	Cleanup(ctx context.Context, namespace string, clientset kubernetes.Interface) error
	Run(ctx context.Context, namespace string, clientset kubernetes.Interface, executor ActionExecutor) error
}

func RunScenario(
	ctx context.Context,
	scenario TestScenario,
	actioner ActionExecutor,
	logger *slog.Logger,
	clientset kubernetes.Interface,
) error {
	//nolint:gosec // No point to use crypto/rand.
	namespaceForTest := fmt.Sprintf("test-namespace-%d", rand.Int31())
	logger = logger.With("namespace", namespaceForTest, "scenario", scenario.Name())

	// Prepare the namespace to run the test in.
	logger.Info("Preparing namespace for test")
	_, err := clientset.CoreV1().Namespaces().Get(ctx, namespaceForTest, metav1.GetOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to get namespace for test %v: %w", namespaceForTest, err)
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("namespace %v already exists and could be in use, cannot continue", namespaceForTest)
	}

	logger.Info("Namespace does not exist, will create")
	_, err = clientset.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespaceForTest,
		},
	}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create namespace %v: %w", namespaceForTest, err)
	}
	defer func() {
		// Cleanup uses different context so it runs even when the overall one is already cancelled
		ctxForCleanup, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		logger.Info("Deleting namespace for test")
		err := clientset.CoreV1().Namespaces().Delete(ctxForCleanup, namespaceForTest, metav1.DeleteOptions{
			GracePeriodSeconds: lo.ToPtr(int64(0)),
			PropagationPolicy:  lo.ToPtr(metav1.DeletePropagationBackground),
		})
		if err != nil {
			logger.Error(fmt.Sprintf("Failed to delete namespace for test %v: %v", namespaceForTest, err))
			return
		}
		logger.Info("Successfully deleted namespace for test")
	}()
	logger.Info("Namespace created")

	logger.Info("Starting test scenario")

	logger.Info("Running preparation function")
	// We defer the cleanup before running preparation or run because each can "fail" in the middle and leave hanging resources.
	defer func() {
		// Cleanup uses different context so it runs even when the overall one is already cancelled
		ctxForCleanup, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		logger.Info("Running cleanup function")
		err := scenario.Cleanup(ctxForCleanup, namespaceForTest, clientset)
		if err != nil {
			logger.Error("failed ot run cleanup", "error", err)
		}
	}()

	err = scenario.Preparation(ctx, namespaceForTest, clientset)
	if err != nil {
		logger.Warn("Preparation for scenario failed", "error", err)
		return fmt.Errorf("failed to run preparation function: %w", err)
	}

	scenarioCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()

	logger.Info("Starting scenario execution")
	err = scenario.Run(scenarioCtx, namespaceForTest, clientset, actioner)
	if err != nil {
		return fmt.Errorf("failed to run scenario: %w", err)
	}

	return nil
}
