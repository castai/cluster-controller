package actions

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/castai/cluster-controller/internal/castai"
	"github.com/castai/cluster-controller/internal/k8s"
	"github.com/castai/cluster-controller/internal/logger"
	"github.com/castai/cluster-controller/internal/nodes"
	"github.com/castai/cluster-controller/internal/volume"
)

var _ ActionHandler = &DrainNodeInfomerHandler{}

func NewDrainNodeInformerHandler(
	log logrus.FieldLogger,
	clientset kubernetes.Interface,
	castNamespace string,
	vaWaiter volume.DetachmentWaiter,
	nodeManager nodes.Manager,
) *DrainNodeInfomerHandler {
	return &DrainNodeInfomerHandler{
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
		nodeManager: nodeManager,
		client:      k8s.NewClient(clientset, log),
	}
}

type DrainNodeInfomerHandler struct {
	log         logrus.FieldLogger
	clientset   kubernetes.Interface
	vaWaiter    volume.DetachmentWaiter
	cfg         drainNodeConfig
	nodeManager nodes.Manager
	client      *k8s.Client
}

func (h *DrainNodeInfomerHandler) Handle(ctx context.Context, action *castai.ClusterAction) error {
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

	ctx = logger.WithLogger(ctx, log)

	log.Info("draining kubernetes node")
	if req.NodeName == "" ||
		(req.NodeID == "" && req.ProviderId == "") {
		return fmt.Errorf("node name or node ID/provider ID is empty %w", k8s.ErrAction)
	}

	node, err := h.client.GetNodeByIDs(ctx, req.NodeName, req.NodeID, req.ProviderId)
	if errors.Is(err, k8s.ErrNodeNotFound) || errors.Is(err, k8s.ErrNodeDoesNotMatch) {
		log.Info("node not found, skipping draining")
		return nil
	}
	if err != nil {
		return err
	}

	log.Info("cordoning node for draining")

	if err = h.client.CordonNode(ctx, node); err != nil {
		return fmt.Errorf("cordoning node %q: %w", req.NodeName, err)
	}

	log.Infof("draining node, drain_timeout_seconds=%f, force=%v created_at=%s", drainTimeout.Seconds(), req.Force, action.CreatedAt)

	// First try to evict pods gracefully using eviction API.
	evictCtx, evictCancel := context.WithTimeout(ctx, drainTimeout)
	defer evictCancel()

	nonEvictablePods, err := h.nodeManager.Evict(evictCtx, nodes.EvictRequest{
		Node:                      node.Name,
		SkipDeletedTimeoutSeconds: h.cfg.skipDeletedTimeoutSeconds,
		CastNamespace:             h.cfg.castNamespace,
	})

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
		defer deleteCancel()

		nonEvictablePods, deleteErr = h.nodeManager.Drain(deleteCtx, nodes.DrainRequest{
			Node:                      node.Name,
			CastNamespace:             h.cfg.castNamespace,
			SkipDeletedTimeoutSeconds: h.cfg.skipDeletedTimeoutSeconds,
			DeleteOptions:             o,
		})

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
func (h *DrainNodeInfomerHandler) waitForVolumeDetachIfEnabled(ctx context.Context, log logrus.FieldLogger, nodeName string, req *castai.ActionDrainNode, nonEvictablePods []*v1.Pod) {
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

func logCastPodsToEvict(log logrus.FieldLogger, castPods []*v1.Pod) {
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
