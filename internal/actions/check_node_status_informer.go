package actions

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	"github.com/castai/cluster-controller/internal/castai"
	"github.com/castai/cluster-controller/internal/informer"
)

var _ ActionHandler = &checkNodeStatusInformerHandler{}

type checkNodeStatusInformerHandler struct {
	log             logrus.FieldLogger
	clientset       kubernetes.Interface
	informerManager *informer.Manager
}

func (h *checkNodeStatusInformerHandler) Handle(ctx context.Context, action *castai.ClusterAction) error {
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

func (h *checkNodeStatusInformerHandler) checkNodeDeleted(ctx context.Context, log *logrus.Entry, req *castai.ActionCheckNodeStatus) error {
	timeout := 10
	if req.WaitTimeoutSeconds != nil {
		timeout = int(*req.WaitTimeoutSeconds)
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	return h.checkNodeDeletedWithInformer(ctx, req.NodeName, req.NodeID, req.ProviderId, log)
}

func (h *checkNodeStatusInformerHandler) checkNodeDeletedWithInformer(ctx context.Context, nodeName, nodeID, providerID string, log logrus.FieldLogger) error {
	if err := h.checkNodeAlreadyDeleted(nodeName, nodeID, providerID, log); err != nil {
		if errors.Is(err, errNodeNotDeleted) {
			return h.waitForNodeDeletion(ctx, nodeName, nodeID, providerID, log)
		}
		return err
	}
	return nil
}

func (h *checkNodeStatusInformerHandler) checkNodeAlreadyDeleted(nodeName, nodeID, providerID string, log logrus.FieldLogger) error {
	lister := h.informerManager.GetNodeLister()
	node, err := lister.Get(nodeName)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			log.Info("node already deleted in cache")
			return nil
		}
		return fmt.Errorf("getting node from lister: %w", err)
	}

	if err := isNodeIDProviderIDValid(node, nodeID, providerID, log); err != nil {
		if errors.Is(err, errNodeDoesNotMatch) {
			log.Info("node name reused, original node deleted")
			return nil
		}
		return fmt.Errorf("validating node ID/provider ID: %w", err)
	}

	return errNodeNotDeleted
}

func (h *checkNodeStatusInformerHandler) waitForNodeDeletion(ctx context.Context, nodeName, nodeID, providerID string, log logrus.FieldLogger) error {
	deleted := make(chan struct{})
	nodeInformer := h.informerManager.GetNodeInformer()

	registration, err := nodeInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: func(oldObj, newObj any) {
			h.handleNodeDeletedUpdateEvent(newObj, nodeName, nodeID, providerID, deleted, log)
		},
		DeleteFunc: func(obj any) {
			h.handleNodeDeletedDeleteEvent(obj, nodeName, deleted, log)
		},
	})
	if err != nil {
		return fmt.Errorf("failed to add event handler: %w", err)
	}
	defer func() {
		if err := nodeInformer.RemoveEventHandler(registration); err != nil {
			log.WithError(err).Warn("failed to remove event handler")
		}
	}()

	select {
	case <-deleted:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("timeout waiting for node to be deleted: %w", ctx.Err())
	}
}

func (h *checkNodeStatusInformerHandler) handleNodeDeletedUpdateEvent(newObj any, nodeName, nodeID, providerID string, deleted chan struct{}, log logrus.FieldLogger) {
	node, ok := newObj.(*corev1.Node)
	if !ok || node.Name != nodeName {
		return
	}
	if err := isNodeIDProviderIDValid(node, nodeID, providerID, log); err != nil {
		if errors.Is(err, errNodeDoesNotMatch) {
			log.Info("node name reused, original node deleted (update event)")
			select {
			case deleted <- struct{}{}:
			default:
			}
		}
	}
}

func (h *checkNodeStatusInformerHandler) handleNodeDeletedDeleteEvent(obj any, nodeName string, deleted chan struct{}, log logrus.FieldLogger) {
	node, ok := obj.(*corev1.Node)
	if !ok {
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
}

func (h *checkNodeStatusInformerHandler) checkNodeReady(ctx context.Context, log *logrus.Entry, req *castai.ActionCheckNodeStatus) error {
	timeout := 9 * time.Minute
	if req.WaitTimeoutSeconds != nil {
		timeout = time.Duration(*req.WaitTimeoutSeconds) * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	lister := h.informerManager.GetNodeLister()
	node, err := lister.Get(req.NodeName)
	if err == nil && h.isNodeReady(node, req.NodeID, req.ProviderId) {
		log.Info("node already ready in cache")
		patchNodeCapacityIfNeeded(ctx, log, h.clientset, node)
		return nil
	}

	ready := make(chan struct{})
	nodeInformer := h.informerManager.GetNodeInformer()

	registration, err := nodeInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			h.handleNodeReadyEvent(obj, req, ready, log, "add event")
		},
		UpdateFunc: func(oldObj, newObj any) {
			h.handleNodeReadyEvent(newObj, req, ready, log, "update event")
		},
	})
	if err != nil {
		return fmt.Errorf("failed to add event handler: %w", err)
	}
	defer func() {
		if err := nodeInformer.RemoveEventHandler(registration); err != nil {
			log.WithError(err).Warn("failed to remove event handler")
		}
	}()

	select {
	case <-ready:
		node, err := lister.Get(req.NodeName)
		if err != nil {
			log.WithError(err).Error("failed to get node, will skip patch")
			return nil
		}
		patchNodeCapacityIfNeeded(ctx, log, h.clientset, node)
		return nil
	case <-ctx.Done():
		return fmt.Errorf("timeout waiting for node to be ready: %w", ctx.Err())
	}
}

func (h *checkNodeStatusInformerHandler) handleNodeReadyEvent(obj any, req *castai.ActionCheckNodeStatus, ready chan struct{}, log *logrus.Entry, eventType string) {
	node, ok := obj.(*corev1.Node)
	if !ok || node.Name != req.NodeName {
		return
	}
	if h.isNodeReady(node, req.NodeID, req.ProviderId) {
		log.Infof("node became ready (%s)", eventType)
		select {
		case ready <- struct{}{}:
		default:
		}
	}
}

func (h *checkNodeStatusInformerHandler) isNodeReady(node *corev1.Node, castNodeID, providerID string) bool {
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
