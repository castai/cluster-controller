package actions

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/sirupsen/logrus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/castai/cluster-controller/castai"
)

type checkNodeDeletedConfig struct {
	retries   uint64
	retryWait time.Duration
}

func newCheckNodeDeletedHandler(log logrus.FieldLogger, clientset kubernetes.Interface) ActionHandler {
	return &checkNodeDeletedHandler{
		log:       log,
		clientset: clientset,
		cfg: checkNodeDeletedConfig{
			retries:   5,
			retryWait: 1 * time.Second,
		},
	}
}

type checkNodeDeletedHandler struct {
	log       logrus.FieldLogger
	clientset kubernetes.Interface
	cfg       checkNodeDeletedConfig
}

func (h *checkNodeDeletedHandler) Handle(ctx context.Context, data interface{}) error {
	req, ok := data.(*castai.ActionCheckNodeDeleted)
	if !ok {
		return fmt.Errorf("unexpected type %T for check node deleted handler", data)
	}

	log := h.log.WithField("node_name", req.NodeName)
	log.Info("checking if node is deleted")

	b := backoff.WithContext(backoff.WithMaxRetries(backoff.NewConstantBackOff(h.cfg.retryWait), h.cfg.retries), ctx)
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
