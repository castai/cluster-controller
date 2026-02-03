package actions

import (
	"context"
	"fmt"
	"reflect"
	"slices"
	"time"

	"github.com/samber/lo"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/castai/cluster-controller/internal/castai"
	"github.com/castai/cluster-controller/internal/k8s"
)

var _ ActionHandler = &CheckNodeStatusHandler{}

func NewCheckNodeStatusHandler(log logrus.FieldLogger, clientset kubernetes.Interface) ActionHandler {
	return &CheckNodeStatusHandler{
		log:                     log,
		clientset:               clientset,
		checkNodeDeletedHandler: NewCheckNodeDeletedHandler(log, clientset),
	}
}

type CheckNodeStatusHandler struct {
	log       logrus.FieldLogger
	clientset kubernetes.Interface

	checkNodeDeletedHandler *CheckNodeDeletedHandler
}

func (h *CheckNodeStatusHandler) Handle(ctx context.Context, action *castai.ClusterAction) error {
	if action == nil {
		return fmt.Errorf("action is nil %w", k8s.ErrAction)
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

	log.Info("checking status of node")
	if req.NodeName == "" ||
		(req.NodeID == "" && req.ProviderId == "") {
		return fmt.Errorf("node name or node ID/provider ID is empty %w", k8s.ErrAction)
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

func (h *CheckNodeStatusHandler) checkNodeReady(ctx context.Context, _ *logrus.Entry, req *castai.ActionCheckNodeStatus) error {
	timeout := 9 * time.Minute
	if req.WaitTimeoutSeconds != nil {
		timeout = time.Duration(*req.WaitTimeoutSeconds) * time.Second
	}

	watchObject := metav1.SingleObject(metav1.ObjectMeta{
		Name: req.NodeName,
	})
	watchObject.TimeoutSeconds = lo.ToPtr(int64(timeout.Seconds()))

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	watch, err := h.clientset.CoreV1().Nodes().Watch(ctx, watchObject)
	if err != nil {
		return fmt.Errorf("creating node watch: %w", err)
	}
	defer watch.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("node %s request timeout: %v %w", req.NodeName, timeout, ctx.Err())
		case r, ok := <-watch.ResultChan():
			if !ok {
				return fmt.Errorf("node %s request timeout: %v %w", req.NodeName, timeout, k8s.ErrNodeWatcherClosed)
			}
			if node, ok := r.Object.(*corev1.Node); ok {
				if isNodeReady(h.log, node, req.NodeID, req.ProviderId) {
					return nil
				}
			}
		}
	}
}

func isNodeReady(log logrus.FieldLogger, node *corev1.Node, castNodeID, providerID string) bool {
	// if node has castai node id label, check if it matches the one we are waiting for
	// if it doesn't match, we can skip this node.
	if err := k8s.IsNodeIDProviderIDValid(node, castNodeID, providerID); err != nil {
		log.WithFields(logrus.Fields{
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
	return slices.Contains(taints, taintCloudProviderUninitialized)
}

var taintCloudProviderUninitialized = corev1.Taint{
	Key:    "node.cloudprovider.kubernetes.io/uninitialized",
	Effect: corev1.TaintEffectNoSchedule,
}
