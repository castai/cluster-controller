package actions

import (
	"context"
	"errors"
	"fmt"
	"reflect"
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
	"github.com/castai/cluster-controller/internal/informer"
	"github.com/castai/cluster-controller/internal/waitext"
)

var _ ActionHandler = &drainNodeInformerHandler{}

type drainNodeInformerHandler struct {
	log             logrus.FieldLogger
	clientset       kubernetes.Interface
	informerManager *informer.Manager
	cfg             drainNodeConfig
}

func (h *drainNodeInformerHandler) Handle(ctx context.Context, action *castai.ClusterAction) error {
	if action == nil {
		return fmt.Errorf("action is nil %w", errAction)
	}
	req, ok := action.Data().(*castai.ActionDrainNode)
	if !ok {
		return newUnexpectedTypeErr(action.Data(), req)
	}
	drainTimeout := getDrainTimeout(action)

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

	if err := cordonNode(ctx, h.log, h.clientset, node); err != nil {
		return fmt.Errorf("cordoning node %q: %w", req.NodeName, err)
	}

	log.Infof("draining node, drain_timeout_seconds=%f, force=%v created_at=%s", drainTimeout.Seconds(), req.Force, action.CreatedAt)

	// Skip graceful eviction if drain timeout is 0 - go straight to force deletion if allowed.
	if drainTimeout <= 0 {
		log.Info("drain timeout is 0, skipping graceful eviction")
		if !req.Force {
			return fmt.Errorf("drain timeout is 0 and force=%v, cannot drain node without force: %w", req.Force, errAction)
		}
	} else {
		// Try to evict pods gracefully using eviction API.
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
	}

	options := []metav1.DeleteOptions{
		{},
		*metav1.NewDeleteOptions(0),
	}

	var deleteErr error
	for _, o := range options {
		deleteCtx, deleteCancel := context.WithTimeout(ctx, h.cfg.podsDeleteTimeout)

		deleteErr = h.deleteNodePods(deleteCtx, log, node, o)

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

	if deleteErr == nil {
		log.Info("node drained forcefully")
	} else {
		log.Warnf("node failed to fully force drain: %v", deleteErr)
	}

	return deleteErr
}

func (h *drainNodeInformerHandler) evictNodePods(ctx context.Context, log logrus.FieldLogger, node *v1.Node) error {
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

func (h *drainNodeInformerHandler) deleteNodePods(ctx context.Context, log logrus.FieldLogger, node *v1.Node, options metav1.DeleteOptions) error {
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

func (h *drainNodeInformerHandler) listNodePodsToEvict(ctx context.Context, log logrus.FieldLogger, node *v1.Node) ([]v1.Pod, error) {
	lister := h.informerManager.GetPodLister()

	pods, err := lister.List(labels.Everything())
	if err != nil {
		return nil, fmt.Errorf("listing pods from cache: %w", err)
	}

	podsOnNode := lo.Filter(pods, func(p *v1.Pod, _ int) bool {
		return p.Spec.NodeName == node.Name
	})

	// Convert []*v1.Pod to []v1.Pod
	podList := make([]v1.Pod, len(podsOnNode))
	for i, p := range podsOnNode {
		podList[i] = *p
	}

	return filterPodsToEvict(podList, h.cfg), nil
}

func (h *drainNodeInformerHandler) waitNodePodsTerminated(ctx context.Context, log logrus.FieldLogger, node *v1.Node, podsToIgnore []*v1.Pod) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	podsToIgnoreLookup := make(map[string]struct{})
	for _, pod := range podsToIgnore {
		podsToIgnoreLookup[fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)] = struct{}{}
	}

	pods, err := h.listNodePodsToEvict(ctx, log, node)
	if err != nil {
		return fmt.Errorf("listing %q pods to be terminated: %w", node.Name, err)
	}

	tracker := newRemainingPodsTracker(pods, podsToIgnoreLookup)

	if tracker.isEmpty() {
		log.Info("no pods to wait for termination")
		return nil
	}

	log.Infof("waiting for %d pods to terminate (using informer watch), %d pods in ignore list", tracker.count(), len(podsToIgnore))

	done := make(chan struct{})
	podInformer := h.informerManager.GetPodInformer()

	registration, err := podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: func(oldObj, newObj any) {
			pod, ok := newObj.(*v1.Pod)
			if !ok || pod.Spec.NodeName != node.Name {
				return
			}

			podKey := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)

			if !tracker.contains(podKey) {
				return
			}

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
		if err := podInformer.RemoveEventHandler(registration); err != nil {
			log.WithError(err).Warn("failed to remove event handler")
		}
	}()

	recheckInterval := h.cfg.podsTerminationWaitRetryDelay
	if recheckInterval <= 0 {
		recheckInterval = 10 * time.Second
	}
	ticker := time.NewTicker(recheckInterval)
	defer ticker.Stop()

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

	if allDone, err := recheckPods(); err == nil && allDone {
		return nil
	}

	for {
		select {
		case <-done:
			return nil
		case <-ticker.C:
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

func (h *drainNodeInformerHandler) evictPod(ctx context.Context, pod v1.Pod, groupVersion schema.GroupVersion) error {
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
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			if apierrors.IsInternalError(err) {
				return false, err
			}
		}

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

func (h *drainNodeInformerHandler) deletePod(ctx context.Context, options metav1.DeleteOptions, pod v1.Pod) error {
	b := waitext.NewConstantBackoff(h.cfg.podDeleteRetryDelay)
	action := func(ctx context.Context) (bool, error) {
		err := h.clientset.CoreV1().Pods(pod.Namespace).Delete(ctx, pod.Name, options)
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

func (t *remainingPodsTracker) remove(podKey string) int {
	t.mu.Lock()
	defer t.mu.Unlock()

	delete(t.pods, podKey)
	return len(t.pods)
}

func (t *remainingPodsTracker) contains(podKey string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.pods[podKey]
}

func (t *remainingPodsTracker) count() int {
	t.mu.Lock()
	defer t.mu.Unlock()

	return len(t.pods)
}

func (t *remainingPodsTracker) isEmpty() bool {
	t.mu.Lock()
	defer t.mu.Unlock()

	return len(t.pods) == 0
}

func (t *remainingPodsTracker) list() []string {
	t.mu.Lock()
	defer t.mu.Unlock()

	pods := make([]string, 0, len(t.pods))
	for podKey := range t.pods {
		pods = append(pods, podKey)
	}
	return pods
}

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
