package actions

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	watchv1 "k8s.io/apimachinery/pkg/watch"

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

func (h *checkNodeStatusHandler) Handle(ctx context.Context, data interface{}) error {
	req, ok := data.(*castai.ActionCheckNodeStatus)
	if !ok {
		return fmt.Errorf("unexpected type %T for check node status handler", data)
	}

	log := h.log.WithFields(logrus.Fields{
		"node_name":   req.NodeName,
		"node_status": req.NodeStatus,
	})

	if req.NodeStatus != castai.ActionCheckNodeStatus_DELETED && req.NodeStatus != castai.ActionCheckNodeStatus_READY {
		return fmt.Errorf("unknown status to check provided node=%s status=%s", req.NodeName, req.NodeStatus)
	}
	timeout := 1 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	watchObject := metav1.SingleObject(metav1.ObjectMeta{Name: req.NodeName})
	if req.WaitTimeoutSeconds != nil {
		timeout = time.Duration(*req.WaitTimeoutSeconds) * time.Second
	}

	timeoutInSec := int64(timeout.Seconds())
	watchObject.TimeoutSeconds = &timeoutInSec
	log.Infof("timeout used %d", timeoutInSec)

	watch, err := h.clientset.CoreV1().Nodes().Watch(ctx, watchObject)
	if err != nil {
		return fmt.Errorf("creating node watch: %w", err)
	}

	for r := range watch.ResultChan() {
		switch req.NodeStatus {
		case castai.ActionCheckNodeStatus_DELETED:
			if r.Type == watchv1.Deleted {
				return nil
			}
		case castai.ActionCheckNodeStatus_READY:
			if node, ok := r.Object.(*corev1.Node); ok {
				for _, c := range node.Status.Conditions {
					if c.Type == corev1.NodeReady && c.Status == corev1.ConditionTrue {
						log.Infof("node %q ready: %v", req.NodeName, c.Message)
						return nil
					}
				}
			}
		}
	}

	return fmt.Errorf("timeout execeed waiting for node %q to be %s", req.NodeName, req.NodeStatus)
}
