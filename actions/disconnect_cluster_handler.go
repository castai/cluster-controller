package actions

import (
	"context"
	"fmt"
	"github.com/castai/cluster-controller/castai"
	"reflect"

	"github.com/sirupsen/logrus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

func newDisconnectClusterHandler(log logrus.FieldLogger, client kubernetes.Interface) ActionHandler {
	return &disconnectClusterHandler{
		log:    log,
		client: client,
	}
}

type disconnectClusterHandler struct {
	log    logrus.FieldLogger
	client kubernetes.Interface
}

func (c *disconnectClusterHandler) Handle(ctx context.Context, action *castai.ClusterAction) error {
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
		"type":           reflect.TypeOf(action.Data().(*castai.ActionDisconnectCluster)).String(),
		actionIDLogField: action.ID,
	})

	log.Infof("deleting namespace %q", ns)
	gracePeriod := int64(0) // Delete immediately.
	if err := c.client.CoreV1().Namespaces().Delete(ctx, ns, metav1.DeleteOptions{GracePeriodSeconds: &gracePeriod}); err != nil {
		return fmt.Errorf("deleting namespace %q: %v", ns, err)
	}

	return nil
}
