package actions

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/samber/lo"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/api/policy/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/kubectl/pkg/drain"

	"github.com/castai/cluster-controller/internal/castai"
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

func NewDrainNodeHandler(log logrus.FieldLogger, clientset kubernetes.Interface, castNamespace string, informerManager *InformerManager) *DrainNodeHandler {
	return &DrainNodeHandler{
		log:             log,
		clientset:       clientset,
		informerManager: informerManager,
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
	log             logrus.FieldLogger
	clientset       kubernetes.Interface
	informerManager *InformerManager
	cfg             drainNodeConfig
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

	err = h.evictNodePods(evictCtx, log, node)

	if err == nil {
		log.Info("node fully drained via graceful eviction")
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

		deleteErr = h.deleteNodePods(deleteCtx, log, node, o)

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

// Return error if at least one pod failed (but don't wait for it!) => to signal if we should do force delete.

// evictNodePods attempts voluntarily eviction for all pods on node.
// This method will wait until all evictable pods on the node either terminate or fail deletion.
// A timeout should be used to avoid infinite waits.
// Errors in calling EVICT for individual pods are accumulated. If at least one pod failed this but termination was successful, an instance of podFailedActionError is returned.
// The method will still wait for termination of other evicted pods first.
// A return value of nil means all pods on the node should be evicted and terminated.
func (h *DrainNodeHandler) evictNodePods(ctx context.Context, log logrus.FieldLogger, node *v1.Node) error {
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
		return err
	}
	if failedPodsError != nil {
		return failedPodsError
	}
	return nil
}

// deleteNodePods deletes the pods running on node. Use options to control if eviction is graceful or forced.
// This method will wait until all evictable pods on the node either terminate or fail deletion.
// A timeout should be used to avoid infinite waits.
// Errors in calling DELETE for individual pods are accumulated. If at least one pod failed this but termination was successful, an instance of podFailedActionError is returned.
// The method will still wait for termination of other deleted pods first.
// A return value of nil means all pods on the node should be deleted and terminated.
func (h *DrainNodeHandler) deleteNodePods(ctx context.Context, log logrus.FieldLogger, node *v1.Node, options metav1.DeleteOptions) error {
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
		return err
	}
	if failedPodsError != nil {
		return failedPodsError
	}
	return nil
}

// listNodePodsToEvict creates a list of pods that are "evictable" on the node.
// The following pods are ignored:
//   - static pods
//   - DaemonSet pods
//   - pods that are already finished (Succeeded or Failed)
//   - pods that were marked for deletion recently (Terminating state); the meaning of "recently" is controlled by config
// This method uses the informer cache instead of making direct API calls.
func (h *DrainNodeHandler) listNodePodsToEvict(ctx context.Context, log logrus.FieldLogger, node *v1.Node) ([]v1.Pod, error) {
	// Use lister to query from local cache instead of API call
	lister := h.informerManager.GetPodLister()

	pods, err := lister.List(labels.Everything())
	if err != nil {
		return nil, fmt.Errorf("listing pods from cache: %w", err)
	}

	// Filter pods that belong to this node
	podsOnNode := lo.Filter(pods, func(p *v1.Pod, _ int) bool {
		return p.Spec.NodeName == node.Name
	})

	podsToEvict := make([]v1.Pod, 0)
	castPods := make([]v1.Pod, 0)
	// Evict CAST PODs as last ones.
	for _, p := range podsOnNode {
		// Skip pods that have been recently removed.
		if !p.ObjectMeta.DeletionTimestamp.IsZero() &&
			int(time.Since(p.ObjectMeta.GetDeletionTimestamp().Time).Seconds()) > h.cfg.skipDeletedTimeoutSeconds {
			continue
		}

		// Skip completed pods. Will be removed during node removal.
		if p.Status.Phase == v1.PodSucceeded || p.Status.Phase == v1.PodFailed {
			continue
		}

		if p.Namespace == h.cfg.castNamespace && !isDaemonSetPod(p) && !isStaticPod(p) {
			castPods = append(castPods, *p)
			continue
		}

		if !isDaemonSetPod(p) && !isStaticPod(p) {
			podsToEvict = append(podsToEvict, *p)
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
// This method uses the informer watch mechanism instead of polling to efficiently detect pod terminations.
func (h *DrainNodeHandler) waitNodePodsTerminated(ctx context.Context, log logrus.FieldLogger, node *v1.Node, podsToIgnore []*v1.Pod) error {
	// Check if context is cancelled before starting any work.
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

	// Get initial list of pods to wait for from cache
	pods, err := h.listNodePodsToEvict(ctx, log, node)
	if err != nil {
		return fmt.Errorf("listing %q pods to be terminated: %w", node.Name, err)
	}

	// Create thread-safe tracker for remaining pods
	tracker := newRemainingPodsTracker(pods, podsToIgnoreLookup)

	if tracker.isEmpty() {
		log.Info("no pods to wait for termination")
		return nil
	}

	log.Infof("waiting for %d pods to terminate (using informer watch), %d pods in ignore list", tracker.count(), len(podsToIgnore))

	// Set up event handler to watch for pod deletions and updates
	done := make(chan struct{})
	informer := h.informerManager.GetPodInformer()

	registration, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: func(oldObj, newObj any) {
			pod, ok := newObj.(*v1.Pod)
			if !ok || pod.Spec.NodeName != node.Name {
				return
			}

			podKey := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)

			if !tracker.contains(podKey) {
				return
			}

			// Check if pod should be removed from waiting list
			if shouldIgnorePod(pod, h.cfg.skipDeletedTimeoutSeconds) {
				remaining := tracker.remove(podKey)
				log.Infof("pod %s terminating/completed, %d remaining", podKey, remaining)

				if remaining == 0 {
					select {
					case done <- struct{}{}:
					default:
					}
				}
			}
		},
		DeleteFunc: func(obj any) {
			pod, ok := obj.(*v1.Pod)
			if !ok {
				// Handle tombstone
				tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
				if !ok {
					return
				}
				pod, ok = tombstone.Obj.(*v1.Pod)
				if !ok {
					return
				}
			}

			if pod.Spec.NodeName != node.Name {
				return
			}

			podKey := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
			remaining := tracker.remove(podKey)

			if remaining == 0 {
				log.Info("all pods terminated")
				select {
				case done <- struct{}{}:
				default:
				}
			} else {
				log.Infof("pod %s deleted, %d remaining", podKey, remaining)
			}
		},
	})
	if err != nil {
		return fmt.Errorf("adding event handler: %w", err)
	}
	defer func() {
		if err := informer.RemoveEventHandler(registration); err != nil {
			log.WithError(err).Warn("failed to remove event handler")
		}
	}()

	// Set up periodic re-check as a fallback in case events are missed
	recheckInterval := h.cfg.podsTerminationWaitRetryDelay
	if recheckInterval <= 0 {
		recheckInterval = 10 * time.Second // Default fallback interval
	}
	ticker := time.NewTicker(recheckInterval)
	defer ticker.Stop()

	// Helper function to re-check pods from cache
	recheckPods := func() (bool, error) {
		pods, err := h.listNodePodsToEvict(ctx, log, node)
		if err != nil {
			return false, err
		}

		changed, count := tracker.update(pods, podsToIgnoreLookup)
		if changed {
			log.Infof("re-check: %d pods remaining", count)
			return count == 0, nil
		}
		return false, nil
	}

	// Do an immediate check in case pods were already deleted
	if allDone, err := recheckPods(); err == nil && allDone {
		return nil
	}

	// Wait for all pods to terminate or timeout
	for {
		select {
		case <-done:
			return nil
		case <-ticker.C:
			// Periodically re-check the actual pod list from cache as fallback
			if allDone, err := recheckPods(); err != nil {
				log.Warnf("failed to re-check pods during termination wait: %v", err)
			} else if allDone {
				return nil
			}
		case <-ctx.Done():
			remainingPods := tracker.list()
			count := tracker.count()
			return fmt.Errorf("timeout waiting for %d pods (%v) to terminate: %w",
				count, remainingPods, ctx.Err())
		}
	}
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

// remainingPodsTracker is a thread-safe tracker for remaining pods during drain operation.
type remainingPodsTracker struct {
	mu   sync.Mutex
	pods map[string]bool
}

// newRemainingPodsTracker creates a new tracker with the given initial pods.
func newRemainingPodsTracker(pods []v1.Pod, podsToIgnore map[string]struct{}) *remainingPodsTracker {
	tracker := &remainingPodsTracker{
		pods: make(map[string]bool),
	}
	for _, p := range pods {
		podKey := fmt.Sprintf("%s/%s", p.Namespace, p.Name)
		if _, ignored := podsToIgnore[podKey]; !ignored {
			tracker.pods[podKey] = true
		}
	}
	return tracker
}

// remove removes a pod from the tracker and returns the new count.
func (t *remainingPodsTracker) remove(podKey string) int {
	t.mu.Lock()
	defer t.mu.Unlock()

	delete(t.pods, podKey)
	return len(t.pods)
}

// contains checks if a pod is being tracked.
func (t *remainingPodsTracker) contains(podKey string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.pods[podKey]
}

// count returns the number of remaining pods.
func (t *remainingPodsTracker) count() int {
	t.mu.Lock()
	defer t.mu.Unlock()

	return len(t.pods)
}

// isEmpty returns true if there are no remaining pods.
func (t *remainingPodsTracker) isEmpty() bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	return len(t.pods) == 0
}

// list returns a copy of the remaining pod keys.
func (t *remainingPodsTracker) list() []string {
	t.mu.Lock()
	defer t.mu.Unlock()

	pods := make([]string, 0, len(t.pods))
	for podKey := range t.pods {
		pods = append(pods, podKey)
	}
	return pods
}

// update replaces the tracked pods with a new set, given the ignored pods.
// Returns true if the set changed and the new count.
func (t *remainingPodsTracker) update(newPods []v1.Pod, podsToIgnore map[string]struct{}) (changed bool, count int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	newPodsMap := make(map[string]bool)
	for _, p := range newPods {
		podKey := fmt.Sprintf("%s/%s", p.Namespace, p.Name)
		if _, ignored := podsToIgnore[podKey]; !ignored && t.pods[podKey] {
			newPodsMap[podKey] = true
		}
	}

	changed = len(newPodsMap) != len(t.pods)
	if changed {
		t.pods = newPodsMap
	}
	return changed, len(t.pods)
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
// Returns true if the pod is marked for deletion and past grace period, or if it's in a terminal phase.
func shouldIgnorePod(pod *v1.Pod, skipDeletedTimeoutSeconds int) bool {
	// Pod is marked for deletion and past grace period
	if !pod.ObjectMeta.DeletionTimestamp.IsZero() &&
		int(time.Since(pod.ObjectMeta.GetDeletionTimestamp().Time).Seconds()) > skipDeletedTimeoutSeconds {
		return true
	}

	// Pod is in terminal phase
	if pod.Status.Phase == v1.PodSucceeded || pod.Status.Phase == v1.PodFailed {
		return true
	}

	return false
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
