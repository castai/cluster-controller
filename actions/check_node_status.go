package actions

import (
	"context"
	"errors"
	"fmt"
	"math"
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

func (h *checkNodeStatusHandler) Handle(ctx context.Context, data interface{}) error {
	req, ok := data.(*castai.ActionCheckNodeStatus)
	if !ok {
		return fmt.Errorf("unexpected type %T for check node status handler", data)
	}

	log := h.log.WithFields(logrus.Fields{
		"node_name":   req.NodeName,
		"node_status": req.NodeStatus,
	})

	if req.NodeStatus == castai.ActionCheckNodeStatus_DELETED {
		log.Info("checking if node is deleted")
		timeout := int32(5)
		if req.WaitTimeoutSeconds != nil {
			timeout = *req.WaitTimeoutSeconds
		}
		retries := uint64(math.Round(float64(timeout / 1)))

		b := backoff.WithContext(backoff.WithMaxRetries(backoff.NewConstantBackOff(time.Second), retries), ctx)
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
	} else if req.NodeStatus == castai.ActionCheckNodeStatus_READY {
		// 10 minutes default timeout
		timeout := int32(600)
		if req.WaitTimeoutSeconds != nil {
			timeout = *req.WaitTimeoutSeconds
		}
		retries := uint64(math.Round(float64(timeout / 30)))
		log.Infof("checking if node is ready retries: %d", retries)
		b := backoff.WithContext(backoff.WithMaxRetries(backoff.NewConstantBackOff(time.Second*30), retries), ctx)
		return backoff.Retry(func() error {
			n, err := h.clientset.CoreV1().Nodes().Get(ctx, req.NodeName, metav1.GetOptions{})
			if err != nil {
				return err
			}

			for _, cond := range n.Status.Conditions {
				if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
					return nil
				}
			}
			return err
		}, b)

	}

	return fmt.Errorf("unknown status to check provided node=%s status=%s", req.NodeName, req.NodeStatus)
}
