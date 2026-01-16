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

func NewCheckNodeStatusInformerHandler(log logrus.FieldLogger, clientset kubernetes.Interface, nodeInformer informer.NodeInformer) ActionHandler {
	return &checkNodeStatusInformerHandler{
		log:                     log,
		clientset:               clientset,
		informer:                nodeInformer,
		checkNodeDeletedHandler: NewCheckNodeDeletedHandler(log, clientset),
	}
}

type checkNodeStatusInformerHandler struct {
	log       logrus.FieldLogger
	clientset kubernetes.Interface
	informer  informer.NodeInformer

	checkNodeDeletedHandler *CheckNodeDeletedHandler
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
		return h.checkNodeDeletedHandler.Handle(ctx, &castai.ClusterAction{
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

	ready := h.informer.Wait(ctx, req.NodeName, func(node *corev1.Node) (bool, error) {
		return isNodeReady(log, node, req.NodeID, req.ProviderId), nil
	})

	select {
	case err := <-ready:
		if err != nil {
			h.log.WithField("node_id", req.NodeID).Errorf("checking node ready error: %v", err)
			return err
		}
		h.log.WithField("node_id", req.NodeID).Infof("node becamed ready from informer")
		return nil
	case <-ctx.Done():
		return fmt.Errorf("timeout waiting for node to be ready: %w", ctx.Err())
	}
}
