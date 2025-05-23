package actions

import (
	"context"
	"fmt"

	"github.com/samber/lo"
	"github.com/sirupsen/logrus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	apitypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"

	"github.com/castai/cluster-controller/internal/castai"
)

var _ ActionHandler = &PatchHandler{}

type PatchHandler struct {
	log    logrus.FieldLogger
	client dynamic.Interface
}

func NewPatchHandler(log logrus.FieldLogger, client dynamic.Interface) *PatchHandler {
	return &PatchHandler{
		log:    log,
		client: client,
	}
}

func (h *PatchHandler) Handle(ctx context.Context, action *castai.ClusterAction) error {
	req, ok := action.Data().(*castai.ActionPatch)
	if !ok {
		return newUnexpectedTypeErr(action.Data(), req)
	}

	patchType, err := getPatchType(req.PatchType)
	if err != nil {
		return err
	}

	log := h.log.WithFields(logrus.Fields{
		ActionIDLogField: action.ID,
		"action":         action.GetType(),
		"gvr":            req.ID.GroupVersionResource.String(),
		"name":           req.ID.Name,
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
