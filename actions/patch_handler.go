package actions

import (
	"context"
	"fmt"
	"reflect"

	"github.com/samber/lo"
	"github.com/sirupsen/logrus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	apitypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"

	"github.com/castai/cluster-controller/castai"
)

type patchHandler struct {
	log    logrus.FieldLogger
	client dynamic.Interface
}

func newPatchHandler(log logrus.FieldLogger, client dynamic.Interface) ActionHandler {
	return &patchHandler{
		log:    log,
		client: client,
	}
}

func (h *patchHandler) Handle(ctx context.Context, action *castai.ClusterAction) error {
	req, ok := action.Data().(*castai.ActionPatch)
	if !ok {
		return newUnexpectedTypeErr(action.Data(), req)
	}

	patchType, err := getPatchType(req.PatchType)
	if err != nil {
		return err
	}

	log := h.log.WithFields(logrus.Fields{
		"id":       action.ID,
		"action":   reflect.TypeOf(action.Data()).String(),
		"group":    req.ID.Group,
		"resource": req.ID.Resource,
		"version":  req.ID.Version,
		"name":     req.ID.Name,
	})
	if req.ID.Namespace != nil {
		log = log.WithField("namespace", *req.ID.Namespace)
	}

	gvkResource := h.client.Resource(schema.GroupVersionResource{
		Group:    req.ID.Group,
		Version:  req.ID.Version,
		Resource: req.ID.Resource,
	})

	var resource dynamic.ResourceInterface = gvkResource
	if req.ID.Namespace != nil {
		resource = gvkResource.Namespace(*req.ID.Namespace)
	}

	if _, err = resource.Patch(ctx, req.ID.Name, patchType, []byte(req.Patch), metav1.PatchOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("resource not found, skipping patch")
			return nil
		}

		return fmt.Errorf("patching resource %v: %w", req.ID.Resource, err)
	}

	return nil
}

func getPatchType(val string) (apitypes.PatchType, error) {
	if lo.Contains([]apitypes.PatchType{
		apitypes.JSONPatchType,
		apitypes.MergePatchType,
		apitypes.StrategicMergePatchType,
	}, apitypes.PatchType(val)) {
		return apitypes.PatchType(val), nil
	}

	return "", fmt.Errorf("unknown patch type: %v", val)
}
