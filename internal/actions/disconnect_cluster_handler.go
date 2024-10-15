package actions

import (
	"context"
	"fmt"
	"reflect"

	"github.com/castai/cluster-controller/internal/types"
	"github.com/sirupsen/logrus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

var _ ActionHandler = &DisconnectClusterHandler{}

func NewDisconnectClusterHandler(log logrus.FieldLogger, client kubernetes.Interface) *DisconnectClusterHandler {
	return &DisconnectClusterHandler{
		log:    log,
		client: client,
	}
}

type DisconnectClusterHandler struct {
	log    logrus.FieldLogger
	client kubernetes.Interface
}

func (c *DisconnectClusterHandler) Handle(ctx context.Context, action *types.ClusterAction) error {
	ns := "castai-agent"
	_, err := c.client.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}

		// Skip if unauthorized. We either deleted access in previous reconcile loop or we never had it.
		if apierrors.IsUnauthorized(err) {
			return nil
		}

		return err
	}
	log := c.log.WithFields(logrus.Fields{
		"type":           reflect.TypeOf(action.Data().(*types.ActionDisconnectCluster)).String(),
		ActionIDLogField: action.ID,
	})

	log.Infof("deleting namespace %q", ns)
	gracePeriod := int64(0) // Delete immediately.
	if err := c.client.CoreV1().Namespaces().Delete(ctx, ns, metav1.DeleteOptions{GracePeriodSeconds: &gracePeriod}); err != nil {
		return fmt.Errorf("deleting namespace %q: %v", ns, err)
	}

	return nil
}
