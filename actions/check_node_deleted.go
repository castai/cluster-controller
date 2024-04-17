package actions

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/sirupsen/logrus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"

	"github.com/castai/cluster-controller/castai"
	"github.com/castai/cluster-controller/internal/waitext"
)

type checkNodeDeletedConfig struct {
	retries   int
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

func (h *checkNodeDeletedHandler) Handle(ctx context.Context, action *castai.ClusterAction) error {
	req, ok := action.Data().(*castai.ActionCheckNodeDeleted)
	if !ok {
		return fmt.Errorf("unexpected type %T for check node deleted handler", action.Data())
	}

	log := h.log.WithFields(logrus.Fields{
		"node_name":      req.NodeName,
		"node_id":        req.NodeID,
		"type":           reflect.TypeOf(action.Data().(*castai.ActionCheckNodeDeleted)).String(),
		actionIDLogField: action.ID,
	})
	log.Info("checking if node is deleted")

	boff := waitext.WithRetry(waitext.NewConstantBackoff(h.cfg.retryWait), h.cfg.retries)
	handleImpl := waitext.WithTransientRetries(func() error {
		n, err := h.clientset.CoreV1().Nodes().Get(ctx, req.NodeName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}

		if n == nil {
			return nil
		}

		currentNodeID, ok := n.Labels[castai.LabelNodeID]
		if !ok {
			log.Info("node doesn't have castai node id label")
		}
		if currentNodeID != "" {
			if currentNodeID != req.NodeID {
				log.Info("node name was reused. Original node is deleted")
				return nil
			}
			if currentNodeID == req.NodeID {
				return waitext.NewNonTransientError(errors.New("node is not deleted"))
			}
		}

		if n != nil {
			return waitext.NewNonTransientError(errors.New("node is not deleted"))
		}

		return err
	}, func(err error) {
		log.Warnf("node deletion check failed, will retry: %v", err)
	}).WithContext()

	return wait.ExponentialBackoffWithContext(ctx, boff, handleImpl)
}
