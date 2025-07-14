package actions

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/samber/lo"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/typed/core/v1"

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

	log.Info("checking status of node")
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

	b := waitext.DefaultExponentialBackoff()
	return waitext.Retry(
		ctx,
		b,
		waitext.Forever,
		func(ctx context.Context) (bool, error) {
			return checkNodeDeleted(ctx, h.clientset.CoreV1().Nodes(), req.NodeName, req.NodeID, req.ProviderId, log)
		},
		func(err error) {
			log.Warnf("check node %s status failed, will retry: %v", req.NodeName, err)
		},
	)
}

func checkNodeDeleted(ctx context.Context, clientSet v1.NodeInterface, nodeName, nodeID, providerID string, log logrus.FieldLogger) (bool, error) {
	// If node is nil - deleted
	// If label is present and doesn't match - node was reused - deleted
	// If label is present and matches - node is not deleted
	// all other use cases can be found in tests
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
				return fmt.Errorf("node %s request timeout: %v %w", req.NodeName, timeout, errNodeWatcherClosed)
			}
			if node, ok := r.Object.(*corev1.Node); ok {
				if h.isNodeReady(node, req.NodeID, req.ProviderId) {
					return nil
				}
			}
		}
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
