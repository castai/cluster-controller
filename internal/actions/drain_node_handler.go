package actions

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/samber/lo"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/castai/cluster-controller/internal/castai"
	"github.com/castai/cluster-controller/internal/informer"
	"github.com/castai/cluster-controller/internal/waitext"
)

const (
	minDrainTimeout = 0 // Minimal pod drain timeout.
)

type drainNodeConfig struct {
	podsDeleteTimeout             time.Duration
	podDeleteRetries              int
	podDeleteRetryDelay           time.Duration
	podEvictRetryDelay            time.Duration
	podsTerminationWaitRetryDelay time.Duration
	castNamespace                 string
	skipDeletedTimeoutSeconds     int
}

func newDrainNodeConfig(castNamespace string) drainNodeConfig {
	return drainNodeConfig{
		podsDeleteTimeout:             2 * time.Minute,
		podDeleteRetries:              5,
		podDeleteRetryDelay:           5 * time.Second,
		podEvictRetryDelay:            5 * time.Second,
		podsTerminationWaitRetryDelay: 10 * time.Second,
		castNamespace:                 castNamespace,
		skipDeletedTimeoutSeconds:     60,
	}
}

// NewDrainNodeHandler creates a handler for draining nodes.
// If informerManager is provided, it uses efficient informer-based watching.
// If informerManager is nil, it falls back to polling-based implementation.
func NewDrainNodeHandler(log logrus.FieldLogger, clientset kubernetes.Interface, castNamespace string, informerManager *informer.Manager) ActionHandler {
	cfg := newDrainNodeConfig(castNamespace)

	if informerManager != nil {
		log.Info("using informer-based drain node handler")
		return &drainNodeInformerHandler{
			log:             log,
			clientset:       clientset,
			informerManager: informerManager,
			cfg:             cfg,
		}
	}
	log.Info("using polling-based drain node handler")
	return &drainNodePollingHandler{
		log:       log,
		clientset: clientset,
		cfg:       cfg,
	}
}

// getDrainTimeout returns drain timeout adjusted to action creation time.
// the result is clamped between 0s and the requested timeout.
func getDrainTimeout(action *castai.ClusterAction) time.Duration {
	timeSinceCreated := time.Since(action.CreatedAt)
	requestedTimeout := time.Duration(action.ActionDrainNode.DrainTimeoutSeconds) * time.Second

	drainTimeout := requestedTimeout - timeSinceCreated

	return lo.Clamp(drainTimeout, minDrainTimeout*time.Second, requestedTimeout)
}

// cordonNode marks a node as unschedulable.
func cordonNode(ctx context.Context, log logrus.FieldLogger, clientset kubernetes.Interface, node *v1.Node) error {
	if node.Spec.Unschedulable {
		return nil
	}

	err := patchNode(ctx, log, clientset, node, func(n *v1.Node) {
		n.Spec.Unschedulable = true
	})
	if err != nil {
		return fmt.Errorf("patching node unschedulable: %w", err)
	}
	return nil
}

// filterPodsToEvict filters pods to only include those that should be evicted.
func filterPodsToEvict(pods []v1.Pod, cfg drainNodeConfig) []v1.Pod {
	podsToEvict := make([]v1.Pod, 0)
	castPods := make([]v1.Pod, 0)

	for _, p := range pods {
		// Skip pods that have been recently removed.
		if !p.ObjectMeta.DeletionTimestamp.IsZero() &&
			int(time.Since(p.ObjectMeta.GetDeletionTimestamp().Time).Seconds()) > cfg.skipDeletedTimeoutSeconds {
			continue
		}

		// Skip completed pods. Will be removed during node removal.
		if p.Status.Phase == v1.PodSucceeded || p.Status.Phase == v1.PodFailed {
			continue
		}

		if p.Namespace == cfg.castNamespace && !isDaemonSetPod(&p) && !isStaticPod(&p) {
			castPods = append(castPods, p)
			continue
		}

		if !isDaemonSetPod(&p) && !isStaticPod(&p) {
			podsToEvict = append(podsToEvict, p)
		}
	}

	podsToEvict = append(podsToEvict, castPods...)
	return podsToEvict
}

func logCastPodsToEvict(log logrus.FieldLogger, castPods []v1.Pod) {
	if len(castPods) == 0 {
		return
	}

	castPodsNames := make([]string, 0, len(castPods))
	for _, p := range castPods {
		castPodsNames = append(castPodsNames, p.Name)
	}
	joinedPodNames := strings.Join(castPodsNames, ", ")

	log.Warnf("evicting CAST AI pods: %s", joinedPodNames)
}

func isDaemonSetPod(p *v1.Pod) bool {
	return isControlledBy(p, "DaemonSet")
}

func isStaticPod(p *v1.Pod) bool {
	return isControlledBy(p, "Node")
}

func isControlledBy(p *v1.Pod, kind string) bool {
	ctrl := metav1.GetControllerOf(p)

	return ctrl != nil && ctrl.Kind == kind
}

// shouldIgnorePod checks if a pod should be ignored for termination wait.
func shouldIgnorePod(pod *v1.Pod, skipDeletedTimeoutSeconds int) bool {
	if !pod.ObjectMeta.DeletionTimestamp.IsZero() &&
		int(time.Since(pod.ObjectMeta.GetDeletionTimestamp().Time).Seconds()) > skipDeletedTimeoutSeconds {
		return true
	}

	if pod.Status.Phase == v1.PodSucceeded || pod.Status.Phase == v1.PodFailed {
		return true
	}

	return false
}

type podFailedActionError struct {
	Action string
	Errors []error
}

func (p *podFailedActionError) Error() string {
	return fmt.Sprintf("action %q: %v", p.Action, errors.Join(p.Errors...))
}

func (p *podFailedActionError) Unwrap() []error {
	return p.Errors
}

// deletePodWithRetry deletes a pod with retries. Shared function for drain and delete node handlers.
func deletePodWithRetry(ctx context.Context, log logrus.FieldLogger, clientset kubernetes.Interface, options metav1.DeleteOptions, pod v1.Pod, cfg drainNodeConfig) error {
	b := waitext.NewConstantBackoff(cfg.podDeleteRetryDelay)
	action := func(ctx context.Context) (bool, error) {
		err := clientset.CoreV1().Pods(pod.Namespace).Delete(ctx, pod.Name, options)
		if err != nil {
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			if apierrors.IsInternalError(err) {
				return false, err
			}
		}
		return true, err
	}
	err := waitext.Retry(ctx, b, cfg.podDeleteRetries, action, func(err error) {
		log.Warnf("deleting pod %s on node %s in namespace %s, will retry: %v", pod.Name, pod.Spec.NodeName, pod.Namespace, err)
	})
	if err != nil {
		return fmt.Errorf("deleting pod %s in namespace %s: %w", pod.Name, pod.Namespace, err)
	}
	return nil
}
