package actions

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/castai/cluster-controller/internal/castai"
	"github.com/castai/cluster-controller/internal/informer"
	"github.com/castai/cluster-controller/internal/k8s"
	"github.com/castai/cluster-controller/internal/logger"
	"github.com/castai/cluster-controller/internal/nodes"
	"github.com/castai/cluster-controller/internal/volume"
)

var _ ActionHandler = &DrainNodeInfomerHandler{}

const (
	defaultPodsDeleteTimeout             = 2 * time.Minute
	defaultPodDeleteRetries              = 5
	defaultPodDeleteRetryDelay           = 5 * time.Second
	defaultPodEvictRetryDelay            = 5 * time.Second
	defaultPodsTerminationWaitRetryDelay = 10 * time.Second
	defaultSkipDeletedTimeoutSeconds     = 60
)

func newDefaultDrainNodeConfig(castNamespace string) drainNodeConfig {
	return drainNodeConfig{
		podsDeleteTimeout:             defaultPodsDeleteTimeout,
		podDeleteRetries:              defaultPodDeleteRetries,
		podDeleteRetryDelay:           defaultPodDeleteRetryDelay,
		podEvictRetryDelay:            defaultPodEvictRetryDelay,
		podsTerminationWaitRetryDelay: defaultPodsTerminationWaitRetryDelay,
		castNamespace:                 castNamespace,
		skipDeletedTimeoutSeconds:     defaultSkipDeletedTimeoutSeconds,
	}
}

func NewDrainNodeInformerHandler(
	log logrus.FieldLogger,
	clientset kubernetes.Interface,
	castNamespace string,
	vaWaiter volume.DetachmentWaiter,
	podInformer informer.PodInformer,
	nodeInformer informer.NodeInformer,
) *DrainNodeInfomerHandler {
	client := k8s.NewClient(clientset, log)
	nodeManager := nodes.NewDrainer(podInformer, client, log, nodes.DrainerConfig{
		PodEvictRetryDelay:            defaultPodEvictRetryDelay,
		PodsTerminationWaitRetryDelay: defaultPodDeleteRetryDelay,
		PodDeleteRetries:              defaultPodDeleteRetries,
	})

	return &DrainNodeInfomerHandler{
		log:          log,
		vaWaiter:     vaWaiter,
		cfg:          newDefaultDrainNodeConfig(castNamespace),
		nodeManager:  nodeManager,
		nodeInformer: nodeInformer,
		client:       client,
	}
}

type DrainNodeInfomerHandler struct {
	log          logrus.FieldLogger
	vaWaiter     volume.DetachmentWaiter
	cfg          drainNodeConfig
	nodeManager  nodes.Drainer
	nodeInformer informer.NodeInformer
	client       k8s.Client
}

func (h *DrainNodeInfomerHandler) Handle(ctx context.Context, action *castai.ClusterAction) error {
	req, err := h.validateAction(action)
	if err != nil {
		return err
	}

	log := h.createDrainNodeLogger(action, req)
	log.Info("draining kubernetes node")

	ctx = logger.WithLogger(ctx, log)
	drainTimeout := k8s.GetDrainTimeout(action)

	node, err := h.getAndValidateNode(ctx, req)
	if err != nil {
		return err
	}
	if node == nil {
		return nil
	}

	if err = h.cordonNode(ctx, node); err != nil {
		return err
	}

	log.Infof("draining node, drain_timeout_seconds=%f, force=%v created_at=%s", drainTimeout.Seconds(), req.Force, action.CreatedAt)

	return h.drainNode(ctx, node.Name, req, drainTimeout)
}

func (h *DrainNodeInfomerHandler) drainNode(ctx context.Context, nodeName string, req *castai.ActionDrainNode, drainTimeout time.Duration) error {
	log := logger.FromContext(ctx, h.log)

	nonEvictablePods, err := h.tryEviction(ctx, nodeName, drainTimeout)
	if err == nil {
		log.Info("node fully drained via graceful eviction")
		h.waitForVolumeDetachIfEnabled(ctx, nodeName, req, nonEvictablePods)
		return nil
	}

	if !req.Force {
		return fmt.Errorf("node failed to drain via graceful eviction, force=%v, timeout=%f, will not force delete pods: %w", req.Force, drainTimeout.Seconds(), err)
	}

	if !h.shouldForceDrain(ctx, err, drainTimeout, req.Force) {
		return fmt.Errorf("evicting node pods: %w", err)
	}

	nonEvictablePods, drainErr := h.forceDrain(ctx, nodeName)
	if drainErr == nil {
		log.Info("node drained forcefully")
		h.waitForVolumeDetachIfEnabled(ctx, nodeName, req, nonEvictablePods)
	} else {
		log.Warnf("node failed to fully force drain: %v", drainErr)
	}

	return drainErr
}

func (h *DrainNodeInfomerHandler) validateAction(action *castai.ClusterAction) (*castai.ActionDrainNode, error) {
	if action == nil {
		return nil, fmt.Errorf("action is nil %w", k8s.ErrAction)
	}

	req, ok := action.Data().(*castai.ActionDrainNode)
	if !ok {
		return nil, newUnexpectedTypeErr(action.Data(), req)
	}

	if req.NodeName == "" || (req.NodeID == "" && req.ProviderId == "") {
		return nil, fmt.Errorf("node name or node ID/provider ID is empty %w", k8s.ErrAction)
	}

	return req, nil
}

func (h *DrainNodeInfomerHandler) createDrainNodeLogger(action *castai.ClusterAction, req *castai.ActionDrainNode) logrus.FieldLogger {
	return h.log.WithFields(logrus.Fields{
		"node_name":      req.NodeName,
		"node_id":        req.NodeID,
		"provider_id":    req.ProviderId,
		"action":         reflect.TypeOf(action.Data().(*castai.ActionDrainNode)).String(),
		ActionIDLogField: action.ID,
	})
}

func (h *DrainNodeInfomerHandler) getAndValidateNode(ctx context.Context, req *castai.ActionDrainNode) (*v1.Node, error) {
	log := logger.FromContext(ctx, h.log)

	// Try to get node from informer cache first
	node, err := h.nodeInformer.Get(req.NodeName)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			// Fallback to API if not in cache
			return h.getNodeFromAPI(ctx, req)
		}
		return nil, err
	}

	if node == nil {
		log.Info("node not found, skipping draining")
		return nil, nil
	}

	if err := k8s.IsNodeIDProviderIDValid(node, req.NodeID, req.ProviderId); err != nil {
		if errors.Is(err, k8s.ErrNodeDoesNotMatch) {
			log.Info("node does not match expected IDs, skipping draining")
			return nil, nil
		}
		return nil, err
	}

	return node, nil
}

func (h *DrainNodeInfomerHandler) getNodeFromAPI(ctx context.Context, req *castai.ActionDrainNode) (*v1.Node, error) {
	log := logger.FromContext(ctx, h.log)
	log.Debug("node not found in cache, fetching directly from API")

	node, err := h.client.GetNodeByIDs(ctx, req.NodeName, req.NodeID, req.ProviderId)
	if err != nil {
		if errors.Is(err, k8s.ErrNodeNotFound) {
			log.Info("node not found in API, skipping draining")
			return nil, nil
		}
		if errors.Is(err, k8s.ErrNodeDoesNotMatch) {
			log.Info("node does not match expected IDs, skipping draining")
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get node from API: %w", err)
	}

	return node, nil
}

func (h *DrainNodeInfomerHandler) cordonNode(ctx context.Context, node *v1.Node) error {
	log := logger.FromContext(ctx, h.log)
	log.Info("cordoning node for draining")
	if err := h.client.CordonNode(ctx, node); err != nil {
		return fmt.Errorf("cordoning node %q: %w", node.Name, err)
	}
	return nil
}

func (h *DrainNodeInfomerHandler) tryEviction(ctx context.Context, nodeName string, timeout time.Duration) ([]*v1.Pod, error) {
	evictCtx, evictCancel := context.WithTimeout(ctx, timeout)
	defer evictCancel()

	return h.nodeManager.Evict(evictCtx, nodes.EvictRequest{
		Node:                      nodeName,
		SkipDeletedTimeoutSeconds: h.cfg.skipDeletedTimeoutSeconds,
		CastNamespace:             h.cfg.castNamespace,
	})
}

func (h *DrainNodeInfomerHandler) shouldForceDrain(ctx context.Context, evictionErr error, drainTimeout time.Duration, force bool) bool {
	log := logger.FromContext(ctx, h.log)

	// Check if error is recoverable through force drain
	var podsFailedEvictionErr *k8s.PodFailedActionError

	if errors.Is(evictionErr, context.DeadlineExceeded) {
		log.Infof("eviction timeout=%f exceeded, force=%v, proceeding with force drain", drainTimeout.Seconds(), force)
		return true
	}

	if errors.As(evictionErr, &podsFailedEvictionErr) {
		log.Infof("some pods failed eviction, force=%v, proceeding with force drain: %v", force, evictionErr)
		return true
	}

	// Unrecoverable errors (e.g., missing permissions, connectivity issues)
	return false
}

func (h *DrainNodeInfomerHandler) forceDrain(ctx context.Context, nodeName string) ([]*v1.Pod, error) {
	deleteOptions := []metav1.DeleteOptions{
		{},
		*metav1.NewDeleteOptions(0),
	}

	var nonEvictablePods []*v1.Pod
	var lastErr error

	for _, opts := range deleteOptions {
		deleteCtx, cancel := context.WithTimeout(ctx, h.cfg.podsDeleteTimeout)
		defer cancel()

		nonEvictablePods, lastErr = h.nodeManager.Drain(deleteCtx, nodes.DrainRequest{
			Node:                      nodeName,
			CastNamespace:             h.cfg.castNamespace,
			SkipDeletedTimeoutSeconds: h.cfg.skipDeletedTimeoutSeconds,
			DeleteOptions:             opts,
		})

		if lastErr == nil {
			return nonEvictablePods, nil
		}

		var podsFailedDeletionErr *k8s.PodFailedActionError
		if errors.Is(lastErr, context.DeadlineExceeded) || errors.As(lastErr, &podsFailedDeletionErr) {
			continue
		}

		return nil, fmt.Errorf("forcefully deleting pods: %w", lastErr)
	}

	return nonEvictablePods, lastErr
}

// waitForVolumeDetachIfEnabled waits for VolumeAttachments to be deleted if the feature is enabled.
// This is called after successful drain to give CSI drivers time to clean up volumes.
// nonEvictablePods are pods that won't be evicted (DaemonSet, static) - their volumes are excluded from waiting.
func (h *DrainNodeInfomerHandler) waitForVolumeDetachIfEnabled(ctx context.Context, nodeName string, req *castai.ActionDrainNode, nonEvictablePods []*v1.Pod) {
	if !ShouldWaitForVolumeDetach(req) || h.vaWaiter == nil {
		return
	}

	log := logger.FromContext(ctx, h.log)

	var timeout time.Duration
	if req.VolumeDetachTimeoutSeconds != nil && *req.VolumeDetachTimeoutSeconds > 0 {
		timeout = time.Duration(*req.VolumeDetachTimeoutSeconds) * time.Second
	}

	err := h.vaWaiter.Wait(ctx, log, volume.DetachmentWaitOptions{
		NodeName:      nodeName,
		Timeout:       timeout,
		PodsToExclude: nonEvictablePods,
	})
	if err != nil {
		log.Warnf("error waiting for volume detachment: %v", err)
	}
}
