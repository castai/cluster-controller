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

	"github.com/castai/cluster-controller/internal/castai"
	"github.com/castai/cluster-controller/internal/volume"
	"github.com/castai/cluster-controller/internal/waitext"
)

var _ ActionHandler = &DrainNodeHandler{}

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

func NewDrainNodeHandler(
	log logrus.FieldLogger,
	clientset kubernetes.Interface,
	castNamespace string,
	vaWaiter volume.DetachmentWaiter,
) *DrainNodeHandler {
	return &DrainNodeHandler{
		log:       log,
		clientset: clientset,
		vaWaiter:  vaWaiter,
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
func (h *DrainNodeHandler) getDrainTimeout(action *castai.ClusterAction) time.Duration {
	timeSinceCreated := time.Since(action.CreatedAt)
	requestedTimeout := time.Duration(action.ActionDrainNode.DrainTimeoutSeconds) * time.Second

	drainTimeout := requestedTimeout - timeSinceCreated

	return lo.Clamp(drainTimeout, minDrainTimeout*time.Second, requestedTimeout)
}

type DrainNodeHandler struct {
	log       logrus.FieldLogger
	clientset kubernetes.Interface
	vaWaiter  volume.DetachmentWaiter
	cfg       drainNodeConfig
}

type nodePods struct {
	toEvict      []v1.Pod
	nonEvictable []v1.Pod
}

func (h *DrainNodeHandler) Handle(ctx context.Context, action *castai.ClusterAction) error {
	if action == nil {
		return fmt.Errorf("action is nil %w", errAction)
	}
	req, ok := action.Data().(*castai.ActionDrainNode)
	if !ok {
		return newUnexpectedTypeErr(action.Data(), req)
	}
	drainTimeout := h.getDrainTimeout(action)

	log := h.log.WithFields(logrus.Fields{
		"node_name":      req.NodeName,
		"node_id":        req.NodeID,
		"provider_id":    req.ProviderId,
		"action":         reflect.TypeOf(action.Data().(*castai.ActionDrainNode)).String(),
		ActionIDLogField: action.ID,
	})

	log.Info("draining kubernetes node")
	if req.NodeName == "" ||
		(req.NodeID == "" && req.ProviderId == "") {
		return fmt.Errorf("node name or node ID/provider ID is empty %w", errAction)
	}

	node, err := getNodeByIDs(ctx, h.clientset.CoreV1().Nodes(), req.NodeName, req.NodeID, req.ProviderId, log)
	if errors.Is(err, errNodeNotFound) || errors.Is(err, errNodeDoesNotMatch) {
		log.Info("node not found, skipping draining")
		return nil
	}
	if err != nil {
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

	nonEvictablePods, err := h.evictNodePods(evictCtx, log, node)

	if err == nil {
		log.Info("node fully drained via graceful eviction")
		h.waitForVolumeDetachIfEnabled(ctx, log, node.Name, req, nonEvictablePods)
		return nil
	}

	if !req.Force {
		return fmt.Errorf("node failed to drain via graceful eviction, force=%v, timeout=%f, will not force delete pods: %w", req.Force, drainTimeout.Seconds(), err)
	}

	var podsFailedEvictionErr *podFailedActionError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		log.Infof("timeout=%f exceeded during pod eviction, force=%v, starting pod deletion", drainTimeout.Seconds(), req.Force)
	case errors.As(err, &podsFailedEvictionErr):
		log.Infof("some pods failed eviction, force=%v, starting pod deletion: %v", req.Force, err)
	default:
		// Expected to be errors where we can't continue at all; e.g. missing permissions or lack of connectivity.
		return fmt.Errorf("evicting node pods: %w", err)
	}

	// If voluntary eviction fails, and we are told to force drain, start deleting pods.
	// Try deleting pods gracefully first, then delete with 0 grace period. PDBs are not respected here.
	options := []metav1.DeleteOptions{
		{},
		*metav1.NewDeleteOptions(0),
	}

	var deleteErr error
	for _, o := range options {
		deleteCtx, deleteCancel := context.WithTimeout(ctx, h.cfg.podsDeleteTimeout)

		nonEvictablePods, deleteErr = h.deleteNodePods(deleteCtx, log, node, o)

		// Clean-up the child context if we got here; no reason to wait for the function to exit.
		deleteCancel()

		if deleteErr == nil {
			break
		}

		var podsFailedDeletionErr *podFailedActionError
		if errors.Is(deleteErr, context.DeadlineExceeded) || errors.As(deleteErr, &podsFailedDeletionErr) {
			continue
		}
		return fmt.Errorf("forcefully deleting pods: %w", deleteErr)
	}

	// Note: if some pods remained even after forced deletion, we'd get an error from last call here.
	if deleteErr == nil {
		log.Info("node drained forcefully")
		h.waitForVolumeDetachIfEnabled(ctx, log, node.Name, req, nonEvictablePods)
	} else {
		log.Warnf("node failed to fully force drain: %v", deleteErr)
	}

	return deleteErr
}

func (h *DrainNodeHandler) cordonNode(ctx context.Context, node *v1.Node) error {
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

// shouldWaitForVolumeDetach returns whether to wait for VolumeAttachments based on per-action config.
// Returns true only if explicitly enabled via action field; defaults to false (disabled).
func (h *DrainNodeHandler) shouldWaitForVolumeDetach(req *castai.ActionDrainNode) bool {
	if req.WaitForVolumeDetach != nil {
		return *req.WaitForVolumeDetach
	}
	return false
}

// waitForVolumeDetachIfEnabled waits for VolumeAttachments to be deleted if the feature is enabled.
// This is called after successful drain to give CSI drivers time to clean up volumes.
// nonEvictablePods are pods that won't be evicted (DaemonSet, static) - their VAs are excluded from waiting.
func (h *DrainNodeHandler) waitForVolumeDetachIfEnabled(ctx context.Context, log logrus.FieldLogger, nodeName string, req *castai.ActionDrainNode, nonEvictablePods []v1.Pod) {
	if !h.shouldWaitForVolumeDetach(req) || h.vaWaiter == nil {
		return
	}

	// Use per-action timeout if set, otherwise waiter will use its default.
	var timeout time.Duration
	if req.VolumeDetachTimeoutSeconds != nil && *req.VolumeDetachTimeoutSeconds > 0 {
		timeout = time.Duration(*req.VolumeDetachTimeoutSeconds) * time.Second
	}

	if err := h.vaWaiter.Wait(ctx, log, volume.DetachmentWaitOptions{
		NodeName:      nodeName,
		Timeout:       timeout,
		PodsToExclude: nonEvictablePods,
	}); err != nil {
		log.Warnf("error waiting for volume detachment: %v", err)
	}
}

// evictNodePods attempts voluntarily eviction for all pods on node.
// This method will wait until all evictable pods on the node either terminate or fail deletion.
// A timeout should be used to avoid infinite waits.
// Errors in calling EVICT for individual pods are accumulated. If at least one pod failed this but termination was successful, an instance of podFailedActionError is returned.
// The method will still wait for termination of other evicted pods first.
// Returns non-evictable pods (DaemonSet, static).
// A return value of (pods, nil) means all evictable pods on the node should be evicted and terminated.
func (h *DrainNodeHandler) evictNodePods(ctx context.Context, log logrus.FieldLogger, node *v1.Node) ([]v1.Pod, error) {
	nodePods, err := h.listNodePods(ctx, log, node)
	if err != nil {
		return nil, err
	}

	if len(nodePods.toEvict) == 0 {
		log.Infof("no pods to evict")
		return nodePods.nonEvictable, nil
	}
	log.Infof("evicting %d pods", len(nodePods.toEvict))
	groupVersion, err := drain.CheckEvictionSupport(h.clientset)
	if err != nil {
		return nil, err
	}
	evictPod := func(ctx context.Context, pod v1.Pod) error {
		return h.evictPod(ctx, pod, groupVersion)
	}

	_, podsWithFailedEviction := executeBatchPodActions(ctx, log, nodePods.toEvict, evictPod, "evict-pod")
	var podsToIgnoreForTermination []*v1.Pod
	var failedPodsError *podFailedActionError
	if len(podsWithFailedEviction) > 0 {
		podErrors := lo.Map(podsWithFailedEviction, func(failure podActionFailure, _ int) error {
			return fmt.Errorf("pod %s/%s failed eviction: %w", failure.pod.Namespace, failure.pod.Name, failure.err)
		})
		failedPodsError = &podFailedActionError{
			Action: "evict",
			Errors: podErrors,
		}
		log.Warnf("some pods failed eviction, will ignore for termination wait: %v", failedPodsError)
		podsToIgnoreForTermination = lo.Map(podsWithFailedEviction, func(failure podActionFailure, _ int) *v1.Pod {
			return failure.pod
		})
	}

	err = h.waitNodePodsTerminated(ctx, log, node, podsToIgnoreForTermination)
	if err != nil {
		return nil, err
	}
	if failedPodsError != nil {
		return nil, failedPodsError
	}
	return nodePods.nonEvictable, nil
}

// deleteNodePods deletes the pods running on node. Use options to control if eviction is graceful or forced.
// This method will wait until all evictable pods on the node either terminate or fail deletion.
// A timeout should be used to avoid infinite waits.
// Errors in calling DELETE for individual pods are accumulated. If at least one pod failed this but termination was successful, an instance of podFailedActionError is returned.
// The method will still wait for termination of other deleted pods first.
// Returns non-evictable pods (DaemonSet, static).
// A return value of (pods, nil) means all evictable pods on the node should be deleted and terminated.
func (h *DrainNodeHandler) deleteNodePods(ctx context.Context, log logrus.FieldLogger, node *v1.Node, options metav1.DeleteOptions) ([]v1.Pod, error) {
	nodePods, err := h.listNodePods(ctx, log, node)
	if err != nil {
		return nil, err
	}

	if len(nodePods.toEvict) == 0 {
		log.Infof("no pods to delete")
		return nodePods.nonEvictable, nil
	}

	if options.GracePeriodSeconds != nil {
		log.Infof("forcefully deleting %d pods with gracePeriod %d", len(nodePods.toEvict), *options.GracePeriodSeconds)
	} else {
		log.Infof("forcefully deleting %d pods", len(nodePods.toEvict))
	}

	deletePod := func(ctx context.Context, pod v1.Pod) error {
		return h.deletePod(ctx, options, pod)
	}

	_, podsWithFailedDeletion := executeBatchPodActions(ctx, log, nodePods.toEvict, deletePod, "delete-pod")
	var podsToIgnoreForTermination []*v1.Pod
	var failedPodsError *podFailedActionError
	if len(podsWithFailedDeletion) > 0 {
		podErrors := lo.Map(podsWithFailedDeletion, func(failure podActionFailure, _ int) error {
			return fmt.Errorf("pod %s/%s failed deletion: %w", failure.pod.Namespace, failure.pod.Name, failure.err)
		})
		failedPodsError = &podFailedActionError{
			Action: "delete",
			Errors: podErrors,
		}
		log.Warnf("some pods failed deletion, will ignore for termination wait: %v", failedPodsError)
		podsToIgnoreForTermination = lo.Map(podsWithFailedDeletion, func(failure podActionFailure, _ int) *v1.Pod {
			return failure.pod
		})
	}

	err = h.waitNodePodsTerminated(ctx, log, node, podsToIgnoreForTermination)
	if err != nil {
		return nil, err
	}
	if failedPodsError != nil {
		return nil, failedPodsError
	}
	return nodePods.nonEvictable, nil
}

// listNodePods lists all pods on a node and categorizes them into evictable and non-evictable.
// This makes a single API call and returns both categories.
// The following pods are considered non-evictable:
//   - static pods
//   - DaemonSet pods
//
// The following pods are filtered out entirely:
//   - pods that are already finished (Succeeded or Failed)
//   - pods that were marked for deletion recently (Terminating state); the meaning of "recently" is controlled by config
func (h *DrainNodeHandler) listNodePods(ctx context.Context, log logrus.FieldLogger, node *v1.Node) (*nodePods, error) {
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

	result := &nodePods{
		toEvict:      make([]v1.Pod, 0),
		nonEvictable: make([]v1.Pod, 0),
	}
	// Evict CAST PODs as last ones.
	castPods := make([]v1.Pod, 0)

	for _, p := range pods.Items {
		// Skip pods that have been recently removed.
		if !p.DeletionTimestamp.IsZero() &&
			int(time.Since(p.ObjectMeta.GetDeletionTimestamp().Time).Seconds()) > h.cfg.skipDeletedTimeoutSeconds {
			continue
		}

		// Skip completed pods. Will be removed during node removal.
		if p.Status.Phase == v1.PodSucceeded || p.Status.Phase == v1.PodFailed {
			continue
		}

		if isDaemonSetPod(&p) || isStaticPod(&p) {
			result.nonEvictable = append(result.nonEvictable, p)
			continue
		}

		if p.Namespace == h.cfg.castNamespace {
			castPods = append(castPods, p)
			continue
		}

		result.toEvict = append(result.toEvict, p)
	}

	logCastPodsToEvict(log, castPods)
	result.toEvict = append(result.toEvict, castPods...)
	return result, nil
}

// waitNodePodsTerminated waits until the pods on the node terminate.
// The wait only considers evictable pods (see listNodePods).
// If podsToIgnore is not empty, the list is further filtered by it.
// This is useful when you don't expect some pods on the node to terminate (e.g. because eviction failed for them) so there is no reason to wait until timeout.
// The wait can potentially run forever if pods are scheduled on the node and are not evicted/deleted by anything. Use a timeout to avoid infinite wait.
func (h *DrainNodeHandler) waitNodePodsTerminated(ctx context.Context, log logrus.FieldLogger, node *v1.Node, podsToIgnore []*v1.Pod) error {
	// Check if context is canceled before starting any work.
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		// Continue with the work.
	}

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
			nodePods, err := h.listNodePods(ctx, log, node)
			if err != nil {
				return true, fmt.Errorf("listing %q pods to be terminated: %w", node.Name, err)
			}

			podsNames := lo.Map(nodePods.toEvict, func(p v1.Pod, _ int) string {
				return fmt.Sprintf("%s/%s", p.Namespace, p.Name)
			})

			remainingPodsList := podsNames
			if len(podsToIgnore) > 0 {
				remainingPodsList = lo.Filter(remainingPodsList, func(podName string, _ int) bool {
					_, ok := podsToIgnoreLookup[podName]
					return !ok
				})
			}
			if remainingPods := len(remainingPodsList); remainingPods > 0 {
				return true, fmt.Errorf("waiting for %d pods (%v) to be terminated on node %v", remainingPods, remainingPodsList, node.Name)
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
func (h *DrainNodeHandler) evictPod(ctx context.Context, pod v1.Pod, groupVersion schema.GroupVersion) error {
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

func (h *DrainNodeHandler) deletePod(ctx context.Context, options metav1.DeleteOptions, pod v1.Pod) error {
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

type podFailedActionError struct {
	// Action holds context what was the code trying to do.
	Action string
	// Errors should hold an entry per pod, for which the action failed.
	Errors []error
}

func (p *podFailedActionError) Error() string {
	return fmt.Sprintf("action %q: %v", p.Action, errors.Join(p.Errors...))
}

func (p *podFailedActionError) Unwrap() []error {
	return p.Errors
}
