package actions

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes"

	"github.com/castai/cluster-controller/internal/castai"
	"github.com/castai/cluster-controller/internal/waitext"
)

var _ ActionHandler = &CheckNodeDeletedHandler{}

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

var errNodeNotDeleted = errors.New("node is not deleted")

func (h *CheckNodeDeletedHandler) Handle(ctx context.Context, action *castai.ClusterAction) error {
	if action == nil {
		return fmt.Errorf("action is nil %w", errAction)
	}
	req, ok := action.Data().(*castai.ActionCheckNodeDeleted)
	if !ok {
		return newUnexpectedTypeErr(action.Data(), req)
	}

	log := h.log.WithFields(logrus.Fields{
		"node_name":      req.NodeName,
		"node_id":        req.NodeID,
		"type":           reflect.TypeOf(action.Data().(*castai.ActionCheckNodeDeleted)).String(),
		"provider_id":    req.ProviderId,
		ActionIDLogField: action.ID,
	})

	log.Info("checking if node is deleted")
	if req.NodeName == "" ||
		(req.NodeID == "" && req.ProviderId == "") {
		return fmt.Errorf("node name %v or node ID: %v or provider ID: %v is empty %w",
			req.NodeName, req.NodeID, req.ProviderId, errAction)
	}

	log.Info("checking if node is deleted")

	boff := waitext.NewConstantBackoff(h.cfg.retryWait)

	return waitext.Retry(
		ctx,
		boff,
		h.cfg.retries,
		func(ctx context.Context) (bool, error) {
			return checkNodeDeleted(ctx, h.clientset.CoreV1().Nodes(), req.NodeName, req.NodeID, req.ProviderId, log)
		},
		func(err error) {
			log.Warnf("node deletion check failed, will retry: %v", err)
		},
	)
}
