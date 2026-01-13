package actions

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/castai/cluster-controller/internal/castai"
	"github.com/castai/cluster-controller/internal/informer"
)

func NewCheckNodeStatusInformerHandler(log logrus.FieldLogger, clientset kubernetes.Interface, informerManager *informer.Manager) ActionHandler {
	return &checkNodeStatusInformerHandler{
		log:             log,
		clientset:       clientset,
		informerManager: informerManager,
	}
}

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
		a := NewCheckNodeDeletedHandler(h.log, h.clientset)
		return a.Handle(ctx, &castai.ClusterAction{
			ActionCheckNodeDeleted: &castai.ActionCheckNodeDeleted{
				NodeName:   req.NodeName,
				ProviderId: req.ProviderId,
				NodeID:     req.NodeID,
			},
		})
	}

	return fmt.Errorf("unknown status to check provided node=%s status=%s", req.NodeName, req.NodeStatus)
}

func (h *checkNodeStatusInformerHandler) checkNodeReady(ctx context.Context, log *logrus.Entry, req *castai.ActionCheckNodeStatus) error {
	timeout := 9 * time.Minute
	if req.WaitTimeoutSeconds != nil {
		timeout = time.Duration(*req.WaitTimeoutSeconds) * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ready := h.informerManager.GetNodeInformer().Wait(ctx, req.NodeName, func(node *corev1.Node) (bool, error) {
		return isNodeReady(log, node, req.NodeID, req.ProviderId), nil
	})

	select {
	case err := <-ready:
		return err
	case <-ctx.Done():
		return fmt.Errorf("timeout waiting for node to be ready: %w", ctx.Err())
	}
}

func (h *checkNodeStatusInformerHandler) handleNodeReadyEvent(obj any, req *castai.ActionCheckNodeStatus, ready chan struct{}, log *logrus.Entry, eventType string) {
	node, ok := obj.(*corev1.Node)
	if !ok || node.Name != req.NodeName {
		return
	}
	if isNodeReady(h.log, node, req.NodeID, req.ProviderId) {
		log.Infof("node became ready (%s)", eventType)
		select {
		case ready <- struct{}{}:
		default:
		}
	}
}
