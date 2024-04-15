package actions

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/castai/cluster-controller/actions/types"
	"github.com/cenkalti/backoff/v4"
	"github.com/sirupsen/logrus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
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

func (h *checkNodeDeletedHandler) Handle(ctx context.Context, action *types.ClusterAction) error {
	req, ok := action.Data().(*types.ActionCheckNodeDeleted)
	if !ok {
		return fmt.Errorf("unexpected type %T for check node deleted handler", action.Data())
	}

	log := h.log.WithFields(logrus.Fields{
		"node_name":      req.NodeName,
		"node_id":        req.NodeID,
		"type":           reflect.TypeOf(action.Data().(*types.ActionCheckNodeDeleted)).String(),
		actionIDLogField: action.ID,
	})
	log.Info("checking if node is deleted")

	b := backoff.WithContext(backoff.WithMaxRetries(backoff.NewConstantBackOff(h.cfg.retryWait), h.cfg.retries), ctx)
	return backoff.Retry(func() error {
		n, err := h.clientset.CoreV1().Nodes().Get(ctx, req.NodeName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}

		if n == nil {
			return nil
		}

		currentNodeID, ok := n.Labels[labelNodeID]
		if !ok {
			log.Info("node doesn't have castai node id label")
		}
		if currentNodeID != "" {
			if currentNodeID != req.NodeID {
				log.Info("node name was reused. Original node is deleted")
				return nil
			}
			if currentNodeID == req.NodeID {
				return backoff.Permanent(errors.New("node is not deleted"))
			}
		}

		if n != nil {
			return backoff.Permanent(errors.New("node is not deleted"))
		}

		return err
	}, b)
}
