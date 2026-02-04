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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"
	"k8s.io/kubectl/pkg/drain"

	"github.com/castai/cluster-controller/internal/castai"
	"github.com/castai/cluster-controller/internal/k8s"
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
		return fmt.Errorf("action is nil %w", k8s.ErrAction)
	}
	req, ok := action.Data().(*castai.ActionDrainNode)
	if !ok {
		return newUnexpectedTypeErr(action.Data(), req)
	}
	drainTimeout := k8s.GetDrainTimeout(action)

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
		return fmt.Errorf("node name or node ID/provider ID is empty %w", k8s.ErrAction)
	}

	node, err := k8s.GetNodeByIDs(ctx, h.clientset.CoreV1().Nodes(), req.NodeName, req.NodeID, req.ProviderId, log)
	if errors.Is(err, k8s.ErrNodeNotFound) || errors.Is(err, k8s.ErrNodeDoesNotMatch) {
		log.Info("node not found, skipping draining")
		return nil
	}
	if err != nil {
		return err
	}

	log.Info("cordoning node for draining")

	if err := k8s.CordonNode(ctx, h.log, h.clientset, node); err != nil {
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

	var podsFailedEvictionErr *k8s.PodFailedActionError
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

		var podsFailedDeletionErr *k8s.PodFailedActionError
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

// waitForVolumeDetachIfEnabled waits for VolumeAttachments to be deleted if the feature is enabled.
// This is called after successful drain to give CSI drivers time to clean up volumes.
// nonEvictablePods are pods that won't be evicted (DaemonSet, static) - their was are excluded from waiting.
func (h *DrainNodeHandler) waitForVolumeDetachIfEnabled(ctx context.Context, log logrus.FieldLogger, nodeName string, req *castai.ActionDrainNode, nonEvictablePods []v1.Pod) {
	if !ShouldWaitForVolumeDetach(req) || h.vaWaiter == nil {
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
		return k8s.EvictPod(ctx, pod, h.cfg.podEvictRetryDelay, h.clientset, h.log, groupVersion)
	}

	_, podsWithFailedEviction := k8s.ExecuteBatchPodActions(ctx, log, nodePods.toEvict, evictPod, "evict-pod")
	var podsToIgnoreForTermination []*v1.Pod
	var failedPodsError *k8s.PodFailedActionError
	if len(podsWithFailedEviction) > 0 {
		podErrors := lo.Map(podsWithFailedEviction, func(failure k8s.PodActionFailure, _ int) error {
			return fmt.Errorf("pod %s/%s failed eviction: %w", failure.Pod.Namespace, failure.Pod.Name, failure.Err)
		})
		failedPodsError = &k8s.PodFailedActionError{
			Action: "evict",
			Errors: podErrors,
		}
		log.Warnf("some pods failed eviction, will ignore for termination wait: %v", failedPodsError)
		podsToIgnoreForTermination = lo.Map(podsWithFailedEviction, func(failure k8s.PodActionFailure, _ int) *v1.Pod {
			return failure.Pod
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
		return k8s.DeletePod(ctx, options, pod, h.cfg.podDeleteRetries, h.cfg.podDeleteRetryDelay, h.clientset, h.log)
	}

	_, podsWithFailedDeletion := k8s.ExecuteBatchPodActions(ctx, log, nodePods.toEvict, deletePod, "delete-pod")
	var podsToIgnoreForTermination []*v1.Pod
	var failedPodsError *k8s.PodFailedActionError
	if len(podsWithFailedDeletion) > 0 {
		podErrors := lo.Map(podsWithFailedDeletion, func(failure k8s.PodActionFailure, _ int) error {
			return fmt.Errorf("pod %s/%s failed deletion: %w", failure.Pod.Namespace, failure.Pod.Name, failure.Err)
		})
		failedPodsError = &k8s.PodFailedActionError{
			Action: "delete",
			Errors: podErrors,
		}
		log.Warnf("some pods failed deletion, will ignore for termination wait: %v", failedPodsError)
		podsToIgnoreForTermination = lo.Map(podsWithFailedDeletion, func(failure k8s.PodActionFailure, _ int) *v1.Pod {
			return failure.Pod
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
		k8s.DefaultBackoff(),
		k8s.DefaultMaxRetriesK8SOperation,
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

	partitioned := k8s.PartitionPodsForEviction(pods.Items, h.cfg.castNamespace, h.cfg.skipDeletedTimeoutSeconds)

	result := &nodePods{
		toEvict:      make([]v1.Pod, 0, len(partitioned.Evictable)+len(partitioned.CastPods)),
		nonEvictable: make([]v1.Pod, 0, len(partitioned.NonEvictable)),
	}

	logCastPodsToEvict(log, partitioned.CastPods)
	result.toEvict = append(result.toEvict, partitioned.Evictable...)
	result.toEvict = append(result.toEvict, partitioned.CastPods...)
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
