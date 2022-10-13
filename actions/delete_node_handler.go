package actions

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/sirupsen/logrus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/castai/cluster-controller/castai"
)

type deleteNodeConfig struct {
	deleteRetries   uint64
	deleteRetryWait time.Duration
}

func newDeleteNodeHandler(log logrus.FieldLogger, clientset kubernetes.Interface) ActionHandler {
	return &deleteNodeHandler{
		log:       log,
		clientset: clientset,
		cfg: deleteNodeConfig{
			deleteRetries:   5,
			deleteRetryWait: 1 * time.Second,
		},
	}
}

type deleteNodeHandler struct {
	log       logrus.FieldLogger
	clientset kubernetes.Interface
	cfg       deleteNodeConfig
}

func (h *deleteNodeHandler) Handle(ctx context.Context, data interface{}, actionID string) error {
	req, ok := data.(*castai.ActionDeleteNode)
	if !ok {
		return fmt.Errorf("unexpected type %T for delete node handler", data)
	}

	log := h.log.WithFields(logrus.Fields{
		"node_name": req.NodeName,
		"node_id":   req.NodeID,
		"type":      reflect.TypeOf(data.(*castai.ActionDeleteNode)).String(),
		"id":        actionID,
	})
	log.Info("deleting kubernetes node")

	b := backoff.WithContext(backoff.WithMaxRetries(backoff.NewConstantBackOff(h.cfg.deleteRetryWait), h.cfg.deleteRetries), ctx)
	return backoff.Retry(func() error {
		err := h.clientset.CoreV1().Nodes().Delete(ctx, req.NodeName, metav1.DeleteOptions{})
		if apierrors.IsNotFound(err) {
			log.Info("node not found, skipping delete")
			return nil
		}
		return err
	}, b)
}
