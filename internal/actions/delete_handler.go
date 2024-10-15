package actions

import (
	"context"
	"fmt"
	"reflect"

	"github.com/sirupsen/logrus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/castai/cluster-controller/internal/types"
)

var _ ActionHandler = &DeleteHandler{}

type DeleteHandler struct {
	log    logrus.FieldLogger
	client dynamic.Interface
}

func NewDeleteHandler(log logrus.FieldLogger, client dynamic.Interface) *DeleteHandler {
	return &DeleteHandler{
		log:    log,
		client: client,
	}
}

func (h *DeleteHandler) Handle(ctx context.Context, action *types.ClusterAction) error {
	req, ok := action.Data().(*types.ActionDelete)
	if !ok {
		return newUnexpectedTypeErr(action.Data(), req)
	}

	log := h.log.WithFields(logrus.Fields{
		"id":     action.ID,
		"action": reflect.TypeOf(action.Data()).String(),
		"gvr":    req.ID.GroupVersionResource.String(),
		"name":   req.ID.Name,
	})

	r := h.client.Resource(schema.GroupVersionResource{
		Group:    req.ID.Group,
		Version:  req.ID.Version,
		Resource: req.ID.Resource,
	})

	var res dynamic.ResourceInterface = r
	if req.ID.Namespace != nil {
		res = r.Namespace(*req.ID.Namespace)
	}

	log.Info("deleting resource")
	if err := res.Delete(ctx, req.ID.Name, metav1.DeleteOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("resource not found, skipping deletion")
			return nil
		}
		return fmt.Errorf("deleting resource %v: %w", req.ID.Name, err)
	}

	return nil
}
