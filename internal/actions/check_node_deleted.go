package actions

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes"
	v1 "k8s.io/client-go/kubernetes/typed/core/v1"

	"github.com/castai/cluster-controller/internal/castai"
	"github.com/castai/cluster-controller/internal/k8s"
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
		return fmt.Errorf("action is nil %w", k8s.ErrAction)
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
			req.NodeName, req.NodeID, req.ProviderId, k8s.ErrAction)
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

func checkNodeDeleted(ctx context.Context, clientSet v1.NodeInterface, nodeName, nodeID, providerID string, log logrus.FieldLogger) (bool, error) {
	// If node is nil - deleted
	// If providerID or label have mismatch, then it's reused and deleted
	// If label is present and matches - node is not deleted
	// All other use cases can be found in tests
	n, err := k8s.GetNodeByIDs(ctx, clientSet, nodeName, nodeID, providerID, log)
	if errors.Is(err, k8s.ErrNodeDoesNotMatch) {
		// it means that node with given name exists, but it does not match requested node ID or provider ID.
		return false, nil
	}

	if errors.Is(err, k8s.ErrNodeNotFound) {
		return false, nil
	}

	if err != nil {
		return true, err
	}

	if n == nil {
		return false, nil
	}

	return false, errNodeNotDeleted
}
