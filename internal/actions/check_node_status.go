package actions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
	v1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"

	"github.com/castai/cluster-controller/internal/castai"
)

var _ ActionHandler = &CheckNodeStatusHandler{}

func NewCheckNodeStatusHandler(log logrus.FieldLogger, clientset kubernetes.Interface, informerManager *InformerManager) *CheckNodeStatusHandler {
	return &CheckNodeStatusHandler{
		log:             log,
		clientset:       clientset,
		informerManager: informerManager,
	}
}

type CheckNodeStatusHandler struct {
	log             logrus.FieldLogger
	clientset       kubernetes.Interface
	informerManager *InformerManager
}

func (h *CheckNodeStatusHandler) Handle(ctx context.Context, action *castai.ClusterAction) error {
	if action == nil {
		return fmt.Errorf("action is nil %w", errAction)
	}
	req, ok := action.Data().(*castai.ActionCheckNodeStatus)
	if !ok {
		return newUnexpectedTypeErr(action.Data(), req)
	}

	log := h.log.WithFields(logrus.Fields{
		"node_name":      req.NodeName,
		"node_id":        req.NodeID,
		"provider_id":    req.ProviderId,
		"node_status":    req.NodeStatus,
		"type":           reflect.TypeOf(action.Data().(*castai.ActionCheckNodeStatus)).String(),
		ActionIDLogField: action.ID,
	})

	if req.NodeName == "" ||
		(req.NodeID == "" && req.ProviderId == "") {
		return fmt.Errorf("node name or node ID/provider ID is empty %w", errAction)
	}

	switch req.NodeStatus {
	case castai.ActionCheckNodeStatus_READY:
		log.Info("checking node ready")
		return h.checkNodeReady(ctx, log, req)
	case castai.ActionCheckNodeStatus_DELETED:
		log.Info("checking node deleted")
		return h.checkNodeDeleted(ctx, log, req)

	}

	return fmt.Errorf("unknown status to check provided node=%s status=%s", req.NodeName, req.NodeStatus)
}

func (h *CheckNodeStatusHandler) checkNodeDeleted(ctx context.Context, log *logrus.Entry, req *castai.ActionCheckNodeStatus) error {
	timeout := 10
	if req.WaitTimeoutSeconds != nil {
		timeout = int(*req.WaitTimeoutSeconds)
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	return h.checkNodeDeletedWithInformer(ctx, req.NodeName, req.NodeID, req.ProviderId, log)
}

func checkNodeDeleted(ctx context.Context, clientSet v1.NodeInterface, nodeName, nodeID, providerID string, log logrus.FieldLogger) (bool, error) {
	// If node is nil - deleted
	// If providerID or label have mismatch, then it's reused and deleted
	// If label is present and matches - node is not deleted
	// All other use cases can be found in tests
	n, err := getNodeByIDs(ctx, clientSet, nodeName, nodeID, providerID, log)
	if errors.Is(err, errNodeDoesNotMatch) {
		// it means that node with given name exists, but it does not match requested node ID or provider ID.
		return false, nil
	}

	if errors.Is(err, errNodeNotFound) {
		return false, nil
	}

	if err != nil {
		return true, err
	}

	if n == nil {
		return false, nil
	}

	return false, errNodeNotDeleted
}

func (h *CheckNodeStatusHandler) checkNodeDeletedWithInformer(ctx context.Context, nodeName, nodeID, providerID string, log logrus.FieldLogger) error {
	lister := h.informerManager.GetNodeLister()

	// Check if node is already deleted in cache
	node, err := lister.Get(nodeName)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			log.Info("node already deleted in cache")
			return nil // Node deleted
		}
		return fmt.Errorf("getting node from lister: %w", err)
	}

	// Check if node ID/provider ID don't match (name reused)
	if err := isNodeIDProviderIDValid(node, nodeID, providerID, log); err != nil {
		if errors.Is(err, errNodeDoesNotMatch) {
			log.Info("node name reused, original node deleted")
			return nil // Name reused, original deleted
		}
		return fmt.Errorf("validating node ID/provider ID: %w", err)
	}

	// Set up channel to receive notification when node is deleted
	deleted := make(chan struct{})
	errCh := make(chan error, 1)

	informer := h.informerManager.GetNodeInformer()

	// Register event handler to watch for node deletion
	registration, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: func(oldObj, newObj any) {
			node, ok := newObj.(*corev1.Node)
			if !ok || node.Name != nodeName {
				return
			}
			// Check if node was replaced (ID mismatch)
			if err := isNodeIDProviderIDValid(node, nodeID, providerID, log); err != nil {
				if errors.Is(err, errNodeDoesNotMatch) {
					log.Info("node name reused, original node deleted (update event)")
					select {
					case deleted <- struct{}{}:
					default:
					}
				}
			}
		},
		DeleteFunc: func(obj any) {
			node, ok := obj.(*corev1.Node)
			if !ok {
				// Handle tombstone case
				tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
				if !ok {
					return
				}
				node, ok = tombstone.Obj.(*corev1.Node)
				if !ok {
					return
				}
			}
			if node.Name == nodeName {
				log.Info("node deleted (delete event)")
				select {
				case deleted <- struct{}{}:
				default:
				}
			}
		},
	})
	if err != nil {
		return fmt.Errorf("failed to add event handler: %w", err)
	}
	defer func() {
		if err := informer.RemoveEventHandler(registration); err != nil {
			log.WithError(err).Warn("failed to remove event handler")
		}
	}()

	// Wait for node to be deleted or timeout
	select {
	case <-deleted:
		return nil
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return fmt.Errorf("timeout waiting for node to be deleted: %w", ctx.Err())
	}
}

func (h *CheckNodeStatusHandler) checkNodeReady(ctx context.Context, log *logrus.Entry, req *castai.ActionCheckNodeStatus) error {
	return h.checkNodeReadyWithInformer(ctx, log, req)
}

func (h *CheckNodeStatusHandler) checkNodeReadyWithInformer(ctx context.Context, log *logrus.Entry, req *castai.ActionCheckNodeStatus) error {
	timeout := 9 * time.Minute
	if req.WaitTimeoutSeconds != nil {
		timeout = time.Duration(*req.WaitTimeoutSeconds) * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Check if node is already ready in cache
	lister := h.informerManager.GetNodeLister()
	node, err := lister.Get(req.NodeName)
	if err == nil && h.isNodeReady(node, req.NodeID, req.ProviderId) {
		log.Info("node already ready in cache")
		return nil
	}

	// Set up channel to receive notification when node is ready
	ready := make(chan struct{})
	errCh := make(chan error, 1)

	informer := h.informerManager.GetNodeInformer()

	// Register event handler to watch for node updates
	registration, err := informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			node, ok := obj.(*corev1.Node)
			if !ok || node.Name != req.NodeName {
				return
			}
			if h.isNodeReady(node, req.NodeID, req.ProviderId) {
				log.Info("node became ready (add event)")
				select {
				case ready <- struct{}{}:
				default:
				}
			}
		},
		UpdateFunc: func(oldObj, newObj any) {
			node, ok := newObj.(*corev1.Node)
			if !ok || node.Name != req.NodeName {
				return
			}
			if h.isNodeReady(node, req.NodeID, req.ProviderId) {
				log.Info("node became ready (update event)")
				select {
				case ready <- struct{}{}:
				default:
				}
			}
		},
	})
	if err != nil {
		return fmt.Errorf("failed to add event handler: %w", err)
	}
	defer func() {
		if err := informer.RemoveEventHandler(registration); err != nil {
			log.WithError(err).Warn("failed to remove event handler")
		}
	}()

	// Wait for node to be ready or timeout
	select {
	case <-ready:
		if err := informer.RemoveEventHandler(registration); err != nil {
			log.WithError(err).Warn("failed to remove event handler")
		}

		bandwith, ok := node.Labels["scheduling.cast.ai/network-bandwidth"]
		if ok {
			patch, _ := json.Marshal(map[string]interface{}{
				"status": map[string]interface{}{
					"capacity": map[string]interface{}{
						"scheduling.cast.ai/network-bandwidth": bandwith,
					},
				},
			})
			log.Infof("going to patch node capacity: %v", node.Name)
			if err := patchNodeStatus(ctx, log, h.clientset, node.Name, patch); err != nil {
				log.WithError(err).Error("failed to patch node capacity")
			}
			log.Infof("patched node capacity: %v", node.Name)
		}
		return nil
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return fmt.Errorf("timeout waiting for node to be ready: %w", ctx.Err())
	}
}

func (h *CheckNodeStatusHandler) isNodeReady(node *corev1.Node, castNodeID, providerID string) bool {
	// if node has castai node id label, check if it matches the one we are waiting for
	// if it doesn't match, we can skip this node.
	if err := isNodeIDProviderIDValid(node, castNodeID, providerID, h.log); err != nil {
		h.log.WithFields(logrus.Fields{
			"node":        node.Name,
			"node_id":     castNodeID,
			"provider_id": providerID,
		}).Warnf("node does not match requested node ID or provider ID: %v", err)
		return false
	}

	for _, cond := range node.Status.Conditions {
		if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue && !containsUninitializedNodeTaint(node.Spec.Taints) {
			return true
		}
	}

	return false
}

func containsUninitializedNodeTaint(taints []corev1.Taint) bool {
	for _, taint := range taints {
		// Some providers like AKS provider adds this taint even if node contains ready condition.
		if taint == taintCloudProviderUninitialized {
			return true
		}
	}
	return false
}

var taintCloudProviderUninitialized = corev1.Taint{
	Key:    "node.cloudprovider.kubernetes.io/uninitialized",
	Effect: corev1.TaintEffectNoSchedule,
}
