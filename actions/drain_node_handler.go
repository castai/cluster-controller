package actions

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/samber/lo"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/api/policy/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/kubectl/pkg/drain"

	"github.com/castai/cluster-controller/castai"
	"github.com/castai/cluster-controller/waitext"
)

const (
	minDrainTimeout = 0 // Minimal pod drain timeout
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

func newDrainNodeHandler(log logrus.FieldLogger, clientset kubernetes.Interface, castNamespace string) ActionHandler {
	return &drainNodeHandler{
		log:       log,
		clientset: clientset,
		cfg: drainNodeConfig{
			podsDeleteTimeout:             2 * time.Minute,
			podDeleteRetries:              5,
			podDeleteRetryDelay:           5 * time.Second,
			podEvictRetryDelay:            5 * time.Second,
			podsTerminationWaitRetryDelay: 10 * time.Second,
			castNamespace:                 castNamespace,
			skipDeletedTimeoutSeconds:     60,
		},
	}
}

// getDrainTimeout returns drain timeout adjusted to action creation time.
// the result is clamped between 0s and the requested timeout.
func (h *drainNodeHandler) getDrainTimeout(action *castai.ClusterAction) time.Duration {
	timeSinceCreated := time.Since(action.CreatedAt)
	requestedTimeout := time.Duration(action.ActionDrainNode.DrainTimeoutSeconds) * time.Second

	drainTimeout := requestedTimeout - timeSinceCreated

	return lo.Clamp(drainTimeout, minDrainTimeout*time.Second, requestedTimeout)
}

type drainNodeHandler struct {
	log       logrus.FieldLogger
	clientset kubernetes.Interface
	cfg       drainNodeConfig
}

func (h *drainNodeHandler) Handle(ctx context.Context, action *castai.ClusterAction) error {
	req, ok := action.Data().(*castai.ActionDrainNode)
	if !ok {
		return fmt.Errorf("unexpected type %T for drain handler", action.Data())
	}
	drainTimeout := h.getDrainTimeout(action)

	log := h.log.WithFields(logrus.Fields{
		"node_name":      req.NodeName,
		"node_id":        req.NodeID,
		"action":         reflect.TypeOf(action.Data().(*castai.ActionDrainNode)).String(),
		actionIDLogField: action.ID,
	})

	node, err := h.clientset.CoreV1().Nodes().Get(ctx, req.NodeName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("node not found, skipping draining")
			return nil
		}
		return err
	}

	log.Info("cordoning node for draining")

	if err := h.cordonNode(ctx, node); err != nil {
		return fmt.Errorf("cordoning node %q: %w", req.NodeName, err)
	}

	log.Infof("draining node, drain_timeout_seconds=%f, force=%v created_at=%s", drainTimeout.Seconds(), req.Force, action.CreatedAt)

	// First try to evict pods gracefully using eviction API.
	evictCtx, evictCancel := context.WithTimeout(ctx, drainTimeout)
	defer evictCancel()

	err = h.evictNodePods(evictCtx, log, node)

	if err == nil {
		log.Info("node fully drained via graceful eviction")
		return nil
	}
	// Individual pod failures shouldn't be returned as error so if we got one, it's more global and we probably can't continue (e.g. missing permissions).
	if !errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("eviciting node pods: %w", err)
	}

	if !req.Force {
		return fmt.Errorf("node failed to drain via graceful eviction, force=%v, timeout=%f, will not force delete pods: %w", req.Force, drainTimeout.Seconds(), err)
	}

	// If voluntary eviction fails and we are told to force drain, start deleting pods.
	// We still allow graceful first and only then force-delete without grace period; but PDBs _can_ be violated here.
	log.Infof("timeout=%f exceeded during pod eviction, force=%v, starting pod deletion", drainTimeout.Seconds(), req.Force)

	// Try deleting pods gracefully first, then delete with 0 grace period.
	options := []metav1.DeleteOptions{
		{},
		*metav1.NewDeleteOptions(0),
	}

	var deleteErr error
	for _, o := range options {
		deleteCtx, deleteCancel := context.WithTimeout(ctx, h.cfg.podsDeleteTimeout)

		deleteErr = h.deleteNodePods(deleteCtx, log, node, o)

		// Clean-up the child context if we got here; no reason to wait for the function to exit
		deleteCancel()

		if deleteErr == nil {
			break
		}
		// Individual delete failures are not surfaced; any error probably means we can't delete at all.
		if !errors.Is(deleteErr, context.DeadlineExceeded) {
			return fmt.Errorf("forcefully deleting pods: %w", deleteErr)
		}
	}

	if deleteErr == nil {
		log.Info("node drained forcefully")
	} else {
		log.Warnf("node failed to force drain: %w", deleteErr)
	}

	return deleteErr
}

func (h *drainNodeHandler) cordonNode(ctx context.Context, node *v1.Node) error {
	if node.Spec.Unschedulable {
		return nil
	}

	err := patchNode(ctx, h.log, h.clientset, node, func(n *v1.Node) {
		n.Spec.Unschedulable = true
	})
	if err != nil {
		return fmt.Errorf("patching node unschedulable: %w", err)
	}
	return nil
}

// evictNodePods attempts voluntarily eviction for all pods on node.
// Errors in evicting individual pods are logged but not surfaced to caller (so it is possible some pods fail to delete even if return value is nil).
// This method will wait until all evictable pods on the node either terminate or fail eviction.
// A timeout should be used to avoid infinite waits.
func (h *drainNodeHandler) evictNodePods(ctx context.Context, log logrus.FieldLogger, node *v1.Node) error {
	pods, err := h.listNodePodsToEvict(ctx, log, node)
	if err != nil {
		return err
	}

	if len(pods) == 0 {
		log.Infof("no pods to evict")
		return nil
	}
	log.Infof("evicting %d pods", len(pods))
	groupVersion, err := drain.CheckEvictionSupport(h.clientset)
	if err != nil {
		return err
	}
	evictPod := func(ctx context.Context, pod v1.Pod) error {
		return h.evictPod(ctx, pod, groupVersion)
	}

	_, podsWithFailedEviction := executeBatchPodActions(ctx, log, pods, evictPod, "evict-pod")
	var podsToIgnoreForTermination []*v1.Pod
	if len(podsWithFailedEviction) > 0 {
		podErrors := lo.Map(podsWithFailedEviction, func(failure podActionFailure, _ int) error {
			return fmt.Errorf("pod %s/%s failed eviction: %w", failure.pod.Namespace, failure.pod.Name, err)
		})
		log.Warnf("some pods failed eviction, will ignore for termination wait: %v", errors.Join(podErrors...))
		podsToIgnoreForTermination = lo.Map(podsWithFailedEviction, func(failure podActionFailure, _ int) *v1.Pod {
			return failure.pod
		})
	}

	return h.waitNodePodsTerminated(ctx, log, node, podsToIgnoreForTermination)
}

// deleteNodePods deletes the pods running on node. Use options to control if eviction is graceful or forced.
// Errors in deleting individual pods are logged but not surfaced to caller (so it is possible some pods fail to delete even if return value is nil).
// This method will wait until all evictable pods on the node either terminate or fail eviction.
// A timeout should be used to avoid infinite waits.
func (h *drainNodeHandler) deleteNodePods(ctx context.Context, log logrus.FieldLogger, node *v1.Node, options metav1.DeleteOptions) error {
	pods, err := h.listNodePodsToEvict(ctx, log, node)
	if err != nil {
		return err
	}

	if len(pods) == 0 {
		log.Infof("no pods to delete")
		return nil
	}

	if options.GracePeriodSeconds != nil {
		log.Infof("forcefully deleting %d pods with gracePeriod %d", len(pods), *options.GracePeriodSeconds)
	} else {
		log.Infof("forcefully deleting %d pods", len(pods))
	}

	deletePod := func(ctx context.Context, pod v1.Pod) error {
		return h.deletePod(ctx, options, pod)
	}

	_, podsWithFailedDeletion := executeBatchPodActions(ctx, log, pods, deletePod, "delete-pod")
	var podsToIgnoreForTermination []*v1.Pod
	if len(podsWithFailedDeletion) > 0 {
		podErrors := lo.Map(podsWithFailedDeletion, func(failure podActionFailure, _ int) error {
			return fmt.Errorf("pod %s/%s failed deletion: %w", failure.pod.Namespace, failure.pod.Name, err)
		})
		log.Warnf("some pods failed deletion, will ignore for termination wait: %v", errors.Join(podErrors...))
		podsToIgnoreForTermination = lo.Map(podsWithFailedDeletion, func(failure podActionFailure, _ int) *v1.Pod {
			return failure.pod
		})
	}

	return h.waitNodePodsTerminated(ctx, log, node, podsToIgnoreForTermination)
}

// listNodePodsToEvict creates a list of pods that are "evictable" on the node.
// The following pods are ignored:
//   - static pods
//   - DaemonSet pods
//   - pods that are already finished (Succeeded or Failed)
//   - pods that were marked for deletion recently (Terminating state); the meaning of "recently" is controlled by config
func (h *drainNodeHandler) listNodePodsToEvict(ctx context.Context, log logrus.FieldLogger, node *v1.Node) ([]v1.Pod, error) {
	var pods *v1.PodList
	err := waitext.Retry(
		ctx,
		defaultBackoff(),
		defaultMaxRetriesK8SOperation,
		func(ctx context.Context) (bool, error) {
			p, err := h.clientset.CoreV1().Pods(metav1.NamespaceAll).List(ctx, metav1.ListOptions{
				FieldSelector: fields.SelectorFromSet(fields.Set{"spec.nodeName": node.Name}).String(),
			})
			if err != nil {
				return true, err
			}
			pods = p
			return false, nil
		},
		func(err error) {
			log.Warnf("listing pods on node %s: %v", node.Name, err)
		},
	)
	if err != nil {
		return nil, fmt.Errorf("listing node %v pods: %w", node.Name, err)
	}

	podsToEvict := make([]v1.Pod, 0)
	castPods := make([]v1.Pod, 0)
	// Evict CAST PODs as last ones
	for _, p := range pods.Items {
		// Skip pods that have been recently removed.
		if !p.ObjectMeta.DeletionTimestamp.IsZero() &&
			int(time.Since(p.ObjectMeta.GetDeletionTimestamp().Time).Seconds()) > h.cfg.skipDeletedTimeoutSeconds {
			continue
		}

		// Skip completed pods. Will be removed during node removal.
		if p.Status.Phase == v1.PodSucceeded || p.Status.Phase == v1.PodFailed {
			continue
		}

		if p.Namespace == h.cfg.castNamespace && !isDaemonSetPod(&p) && !isStaticPod(&p) {
			castPods = append(castPods, p)
			continue
		}

		if !isDaemonSetPod(&p) && !isStaticPod(&p) {
			podsToEvict = append(podsToEvict, p)
		}
	}

	logCastPodsToEvict(log, castPods)
	podsToEvict = append(podsToEvict, castPods...)
	return podsToEvict, nil
}

// waitNodePodsTerminated waits until the pods on the node terminate.
// The wait only considers evictable pods (see listNodePodsToEvict).
// If podsToIgnore is not empty, the list is further filtered by it.
// This is useful when you don't expect some pods on the node to terminate (e.g. because eviction failed for them) so there is no reason to wait until timeout.
// The wait can potentially run forever if pods are scheduled on the node and are not evicted/deleted by anything. Use a timeout to avoid infinite wait.
func (h *drainNodeHandler) waitNodePodsTerminated(ctx context.Context, log logrus.FieldLogger, node *v1.Node, podsToIgnore []*v1.Pod) error {
	podsToIgnoreLookup := make(map[string]struct{})
	for _, pod := range podsToIgnore {
		podsToIgnoreLookup[fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)] = struct{}{}
	}

	log.Infof("starting wait for pod termination, %d pods in ignore list", len(podsToIgnore))
	return waitext.Retry(
		ctx,
		waitext.NewConstantBackoff(h.cfg.podsTerminationWaitRetryDelay),
		waitext.Forever,
		func(ctx context.Context) (bool, error) {
			pods, err := h.listNodePodsToEvict(ctx, log, node)
			if err != nil {
				return true, fmt.Errorf("listing %q pods to be terminated: %w", node.Name, err)
			}

			remainingPods := len(pods)
			if len(podsToIgnore) > 0 {
				for i := range pods {
					_, shouldIgnore := podsToIgnoreLookup[fmt.Sprintf("%s/%s", pods[i].Namespace, pods[i].Name)]
					if shouldIgnore {
						remainingPods--
					}
				}
			}
			if remainingPods > 0 {
				return true, fmt.Errorf("waiting for %d pods to be terminated on node %v", remainingPods, node.Name)
			}
			return false, nil
		},
		func(err error) {
			h.log.Warnf("waiting for pod termination on node %v, will retry: %v", node.Name, err)
		},
	)
}

// evictPod from the k8s node. Error handling is based on eviction api documentation:
// https://kubernetes.io/docs/tasks/administer-cluster/safely-drain-node/#the-eviction-api
func (h *drainNodeHandler) evictPod(ctx context.Context, pod v1.Pod, groupVersion schema.GroupVersion) error {
	b := waitext.NewConstantBackoff(h.cfg.podEvictRetryDelay)
	action := func(ctx context.Context) (bool, error) {
		var err error

		h.log.Debugf("requesting eviction for pod %s/%s", pod.Namespace, pod.Name)
		if groupVersion == policyv1.SchemeGroupVersion {
			err = h.clientset.PolicyV1().Evictions(pod.Namespace).Evict(ctx, &policyv1.Eviction{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pod.Name,
					Namespace: pod.Namespace,
				},
			})
		} else {
			err = h.clientset.CoreV1().Pods(pod.Namespace).EvictV1beta1(ctx, &v1beta1.Eviction{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "policy/v1beta1",
					Kind:       "Eviction",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      pod.Name,
					Namespace: pod.Namespace,
				},
			})
		}

		if err != nil {
			// Pod is not found - ignore.
			if apierrors.IsNotFound(err) {
				return false, nil
			}

			// Pod is misconfigured - stop retry.
			if apierrors.IsInternalError(err) {
				return false, err
			}
		}

		// Other errors - retry.
		// This includes 429 TooManyRequests (due to throttling) and 429 TooManyRequests + DisruptionBudgetCause (due to violated PDBs)
		// This is done to try and do graceful eviction for as long as possible;
		// it is expected that caller has a timeout that will stop this process if the PDB can never be satisfied.
		// Note: pods only receive SIGTERM signals if they are evicted; if PDB prevents that, the signal will not happen here.
		return true, err
	}
	err := waitext.Retry(ctx, b, waitext.Forever, action, func(err error) {
		h.log.Warnf("evict pod %s on node %s in namespace %s, will retry: %v", pod.Name, pod.Spec.NodeName, pod.Namespace, err)
	})
	if err != nil {
		return fmt.Errorf("evicting pod %s in namespace %s: %w", pod.Name, pod.Namespace, err)
	}
	return nil
}

func (h *drainNodeHandler) deletePod(ctx context.Context, options metav1.DeleteOptions, pod v1.Pod) error {
	b := waitext.NewConstantBackoff(h.cfg.podDeleteRetryDelay)
	action := func(ctx context.Context) (bool, error) {
		err := h.clientset.CoreV1().Pods(pod.Namespace).Delete(ctx, pod.Name, options)
		if err != nil {
			// Pod is not found - ignore.
			if apierrors.IsNotFound(err) {
				return false, nil
			}

			// Pod is misconfigured - stop retry.
			if apierrors.IsInternalError(err) {
				return false, err
			}
		}

		// Other errors - retry.
		return true, err
	}
	err := waitext.Retry(ctx, b, h.cfg.podDeleteRetries, action, func(err error) {
		h.log.Warnf("deleting pod %s on node %s in namespace %s, will retry: %v", pod.Name, pod.Spec.NodeName, pod.Namespace, err)
	})
	if err != nil {
		return fmt.Errorf("deleting pod %s in namespace %s: %w", pod.Name, pod.Namespace, err)
	}
	return nil
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
