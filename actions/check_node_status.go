package actions

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/client-go/kubernetes"

	"github.com/castai/cluster-controller/castai"
)

func newCheckNodeStatusHandler(log logrus.FieldLogger, clientset kubernetes.Interface) ActionHandler {
	return &checkNodeStatusHandler{
		log:       log,
		clientset: clientset,
	}
}

type checkNodeStatusHandler struct {
	log       logrus.FieldLogger
	clientset kubernetes.Interface
}

func (h *checkNodeStatusHandler) Handle(ctx context.Context, action *castai.ClusterAction) error {
	req, ok := action.Data().(*castai.ActionCheckNodeStatus)
	if !ok {
		return fmt.Errorf("unexpected type %T for check node status handler", action.Data())
	}

	log := h.log.WithFields(logrus.Fields{
		"node_name":   req.NodeName,
		"node_id":     req.NodeID,
		"node_status": req.NodeStatus,
		"type":        reflect.TypeOf(action.Data().(*castai.ActionCheckNodeStatus)).String(),
		"id":          action.ID,
	})

	switch req.NodeStatus {
	case castai.ActionCheckNodeStatus_READY:
		log.Info("checking node ready")
		return h.checkNodeReady(ctx, req)
	case castai.ActionCheckNodeStatus_DELETED:
		log.Info("checking node deleted")
		return h.checkNodeDeleted(ctx, req)

	}

	return fmt.Errorf("unknown status to check provided node=%s status=%s", req.NodeName, req.NodeStatus)
}

func (h *checkNodeStatusHandler) checkNodeDeleted(ctx context.Context, req *castai.ActionCheckNodeStatus) error {
	timeout := 10
	if req.WaitTimeoutSeconds != nil {
		timeout = int(*req.WaitTimeoutSeconds)
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()
	b := backoff.WithContext(backoff.NewExponentialBackOff(), ctx)
	return backoff.Retry(func() error {
		n, err := h.clientset.CoreV1().Nodes().Get(ctx, req.NodeName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}
		if n != nil {
			return backoff.Permanent(errors.New("node is not deleted"))
		}
		return err
	}, b)
}

func (h *checkNodeStatusHandler) checkNodeReady(ctx context.Context, req *castai.ActionCheckNodeStatus) error {
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
			if isNodeReady(node.Status.Conditions) {
				return nil
			}
		}
	}

	return fmt.Errorf("timeout waiting for node %s to become ready", req.NodeName)
}

func isNodeReady(conditions []corev1.NodeCondition) bool {
	for _, cond := range conditions {
		if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
			return true
		}
	}

	return false
}
