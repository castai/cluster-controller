package scenarios

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/samber/lo"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/castai/cluster-controller/internal/castai"
)

func EvictPod(count int, log *slog.Logger) TestScenario {
	return &evictPodScenario{
		totalPods: count,
		log:       log,
	}
}

type evictPodScenario struct {
	totalPods int
	log       *slog.Logger

	podsToEvict []*v1.Pod
}

func (e *evictPodScenario) Name() string {
	return "evict pod"
}

func (e *evictPodScenario) Preparation(ctx context.Context, namespace string, clientset kubernetes.Interface) error {
	// Create N pods; store in state
	for i := range e.totalPods {
		select {
		case <-ctx.Done():
			return fmt.Errorf("context done: %w", ctx.Err())
		default:
		}

		pod := Pod(fmt.Sprintf("evict-pod-%d", i))
		pod.ObjectMeta.Namespace = namespace

		e.log.Info(fmt.Sprintf("Creating pod %s", pod.Name))
		_, err := clientset.CoreV1().Pods(namespace).Create(ctx, pod, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("creating pod: %w", err)
		}

		e.podsToEvict = append(e.podsToEvict, pod)
	}

	return nil
}

func (e *evictPodScenario) Cleanup(ctx context.Context, namespace string, clientset kubernetes.Interface) error {
	var errs []error

	for _, pod := range e.podsToEvict {
		e.log.Info(fmt.Sprintf("Deleting pod %s", pod.Name))
		err := clientset.CoreV1().Pods(namespace).Delete(ctx, pod.Name, metav1.DeleteOptions{GracePeriodSeconds: lo.ToPtr(int64(0))})
		if err != nil && !apierrors.IsNotFound(err) {
			e.log.Warn(fmt.Sprintf("failed to delete pod: %v", err))
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (e *evictPodScenario) Run(ctx context.Context, namespace string, clientset kubernetes.Interface, executor ActionExecutor) error {
	e.log.Info(fmt.Sprintf("Starting creating %d actions to evict pods", len(e.podsToEvict)))
	actions := make([]castai.ClusterAction, 0, len(e.podsToEvict))
	for _, pod := range e.podsToEvict {
		actions = append(actions, castai.ClusterAction{
			ID: uuid.NewString(),
			ActionEvictPod: &castai.ActionEvictPod{
				Namespace: pod.Namespace,
				PodName:   pod.Name,
			},
		})
	}
	executor.ExecuteActions(ctx, actions)

	return nil
}
