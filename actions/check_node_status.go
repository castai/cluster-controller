package actions

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	watchv1 "k8s.io/apimachinery/pkg/watch"

	"k8s.io/client-go/kubernetes"

	"github.com/castai/cluster-controller/castai"
)

var errWatchTimeout = errors.New("watch timeout")

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
	timeout := 10 * time.Second
	watchObject := metav1.SingleObject(metav1.ObjectMeta{Name: req.NodeName})
	if req.WaitTimeoutSeconds != nil {
		timeout = time.Duration(*req.WaitTimeoutSeconds) * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	timeoutInSec := int64(timeout.Seconds())
	watchObject.TimeoutSeconds = &timeoutInSec
	log.Infof("checking status of node with timeout=%d", timeoutInSec)

	watch, err := h.clientset.CoreV1().Nodes().Watch(ctx, watchObject)
	if err != nil {
		return fmt.Errorf("creating node watch: %w", err)
	}

	errGroup, ctx := errgroup.WithContext(ctx)
	errGroup.Go(func() error {
		for {
			select {
			case r := <-watch.ResultChan():
				switch req.NodeStatus {
				case castai.ActionCheckNodeStatus_DELETED:
					if r.Type == watchv1.Deleted {
						return nil
					}
				case castai.ActionCheckNodeStatus_READY:
					if node, ok := r.Object.(*corev1.Node); ok {
						if isNodeReady(node.Status.Conditions) {
							return nil
						}
					}
				}
			case <-ctx.Done():
				watch.Stop()
				return errWatchTimeout
			}
		}
	})

	err = errGroup.Wait()
	if err == errWatchTimeout {
		log.Infof("watch timeout fallback to check status using get")
		n, err := h.clientset.CoreV1().Nodes().Get(ctx, req.NodeName, metav1.GetOptions{})
		switch req.NodeStatus {
		case castai.ActionCheckNodeStatus_DELETED:
			if apierrors.IsNotFound(err) {
				return nil
			}
			if n != nil {
				return errors.New("node is not deleted")
			}
			return err
		case castai.ActionCheckNodeStatus_READY:
			if err != nil {
				return err
			}

			if isNodeReady(n.Status.Conditions) {
				return nil
			}

			return fmt.Errorf("node %s is not ready", req.NodeName)
		}
	}

	return err
}

func isNodeReady(conditions []corev1.NodeCondition) bool {
	for _, cond := range conditions {
		if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
			return true
		}
	}

	return false
}
