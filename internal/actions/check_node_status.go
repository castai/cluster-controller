package actions

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/castai/cluster-controller/internal/castai"
	"github.com/castai/cluster-controller/internal/waitext"
)

var _ ActionHandler = &CheckNodeStatusHandler{}

func NewCheckNodeStatusHandler(log logrus.FieldLogger, clientset kubernetes.Interface) *CheckNodeStatusHandler {
	return &CheckNodeStatusHandler{
		log:       log,
		clientset: clientset,
	}
}

type CheckNodeStatusHandler struct {
	log       logrus.FieldLogger
	clientset kubernetes.Interface
}

func (h *CheckNodeStatusHandler) Handle(ctx context.Context, action *castai.ClusterAction) error {
	req, ok := action.Data().(*castai.ActionCheckNodeStatus)
	if !ok {
		return newUnexpectedTypeErr(action.Data(), req)
	}

	log := h.log.WithFields(logrus.Fields{
		"node_name":      req.NodeName,
		"node_id":        req.NodeID,
		"node_status":    req.NodeStatus,
		"type":           reflect.TypeOf(action.Data().(*castai.ActionCheckNodeStatus)).String(),
		ActionIDLogField: action.ID,
	})

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

	b := waitext.DefaultExponentialBackoff()
	return waitext.Retry(
		ctx,
		b,
		waitext.Forever,
		func(ctx context.Context) (bool, error) {
			n, err := h.clientset.CoreV1().Nodes().Get(ctx, req.NodeName, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return false, nil
			}

			// If node is nil - deleted
			// If label is present and doesn't match - node was reused - deleted
			// If label is present and matches - node is not deleted
			// If label is not present and node is not nil - node is not deleted (potentially corrupted state).

			if n == nil {
				return false, nil
			}

			currentNodeID, ok := n.Labels[castai.LabelNodeID]
			if !ok {
				log.Info("node doesn't have castai node id label")
			}
			if currentNodeID != "" {
				if currentNodeID != req.NodeID {
					log.Info("node name was reused. Original node is deleted")
					return false, nil
				}
				if currentNodeID == req.NodeID {
					return false, fmt.Errorf("current node id is equal to requested node id: %v %w", req.NodeID, errNodeNotDeleted)
				}
			}

			if n != nil {
				return false, errNodeNotDeleted
			}

			return true, err
		},
		func(err error) {
			h.log.Warnf("check node %s status failed, will retry: %v", req.NodeName, err)
		},
	)
}

func (h *CheckNodeStatusHandler) checkNodeReady(ctx context.Context, _ *logrus.Entry, req *castai.ActionCheckNodeStatus) error {
	timeout := 9 * time.Minute
	watchObject := metav1.SingleObject(metav1.ObjectMeta{Name: req.NodeName})
	if req.WaitTimeoutSeconds != nil {
		timeout = time.Duration(*req.WaitTimeoutSeconds) * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	watch, err := h.clientset.CoreV1().Nodes().Watch(ctx, watchObject)
	if err != nil {
		return fmt.Errorf("creating node watch: %w", err)
	}

	defer watch.Stop()
	for r := range watch.ResultChan() {
		if node, ok := r.Object.(*corev1.Node); ok {
			if isNodeReady(node, req.NodeID) {
				return nil
			}
		}
	}

	return fmt.Errorf("timeout waiting for node %s to become ready", req.NodeName)
}

func isNodeReady(node *corev1.Node, castNodeID string) bool {
	// if node has castai node id label, check if it matches the one we are waiting for
	// if it doesn't match, we can skip this node.
	if val, ok := node.Labels[castai.LabelNodeID]; ok {
		if val != "" && val != castNodeID {
			return false
		}
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
