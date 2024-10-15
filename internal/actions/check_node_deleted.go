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
	"k8s.io/client-go/kubernetes"

	"github.com/castai/cluster-controller/internal/types"
	"github.com/castai/cluster-controller/internal/waitext"
)

var _ types.ActionHandler = &CheckNodeDeletedHandler{}

type checkNodeDeletedConfig struct {
	retries   int
	retryWait time.Duration
}

func NewCheckNodeDeletedHandler(log logrus.FieldLogger, clientset kubernetes.Interface) *CheckNodeDeletedHandler {
	return &CheckNodeDeletedHandler{
		log:       log,
		clientset: clientset,
		cfg: checkNodeDeletedConfig{
			retries:   5,
			retryWait: 1 * time.Second,
		},
	}
}

type CheckNodeDeletedHandler struct {
	log       logrus.FieldLogger
	clientset kubernetes.Interface
	cfg       checkNodeDeletedConfig
}

func (h *CheckNodeDeletedHandler) Handle(ctx context.Context, action *types.ClusterAction) error {
	req, ok := action.Data().(*types.ActionCheckNodeDeleted)
	if !ok {
		return fmt.Errorf("unexpected type %T for check node deleted handler", action.Data())
	}

	log := h.log.WithFields(logrus.Fields{
		"node_name":      req.NodeName,
		"node_id":        req.NodeID,
		"type":           reflect.TypeOf(action.Data().(*types.ActionCheckNodeDeleted)).String(),
		ActionIDLogField: action.ID,
	})
	log.Info("checking if node is deleted")

	boff := waitext.NewConstantBackoff(h.cfg.retryWait)

	return waitext.Retry(
		ctx,
		boff,
		h.cfg.retries,
		func(ctx context.Context) (bool, error) {
			n, err := h.clientset.CoreV1().Nodes().Get(ctx, req.NodeName, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return false, nil
			}

			if n == nil {
				return false, nil
			}

			currentNodeID, ok := n.Labels[types.LabelNodeID]
			if !ok {
				log.Info("node doesn't have castai node id label")
			}
			if currentNodeID != "" {
				if currentNodeID != req.NodeID {
					log.Info("node name was reused. Original node is deleted")
					return false, nil
				}
				if currentNodeID == req.NodeID {
					return false, errors.New("node is not deleted")
				}
			}

			if n != nil {
				return false, errors.New("node is not deleted")
			}

			return true, err
		},
		func(err error) {
			log.Warnf("node deletion check failed, will retry: %v", err)
		},
	)
}
