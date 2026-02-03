package actions

import (
	"context"
	"fmt"

	jsonpatch "github.com/evanphx/json-patch"
	"github.com/sirupsen/logrus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	k8s_types "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"

	"github.com/castai/cluster-controller/internal/castai"
	"github.com/castai/cluster-controller/internal/k8s"
)

var _ ActionHandler = &CreateHandler{}

type CreateHandler struct {
	log    logrus.FieldLogger
	client dynamic.Interface
}

func NewCreateHandler(log logrus.FieldLogger, client dynamic.Interface) *CreateHandler {
	return &CreateHandler{
		log:    log,
		client: client,
	}
}

func (h *CreateHandler) Handle(ctx context.Context, action *castai.ClusterAction) error {
	req, ok := action.Data().(*castai.ActionCreate)
	if !ok {
		return newUnexpectedTypeErr(action.Data(), req)
	}

	if req.Object == nil {
		return fmt.Errorf("object not provided %w", k8s.ErrAction)
	}

	newObj := &unstructured.Unstructured{Object: req.Object}

	log := h.log.WithFields(logrus.Fields{
		ActionIDLogField: action.ID,
		"action":         action.GetType(),
		"gvr":            req.String(),
		"name":           newObj.GetName(),
	})

	gvkResource := h.client.Resource(schema.GroupVersionResource{
		Group:    req.Group,
		Version:  req.Version,
		Resource: req.Resource,
	})

	var resource dynamic.ResourceInterface = gvkResource
	if newObj.GetNamespace() != "" {
		resource = gvkResource.Namespace(newObj.GetNamespace())
	}

	log.Info("creating new resource")
	_, err := resource.Create(ctx, newObj, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("creating resource %v: %w", req.Resource, err)
	}

	if apierrors.IsAlreadyExists(err) {
		log.Info("resource already exists, patching")
		obj, err := resource.Get(ctx, newObj.GetName(), metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("getting old resource: %w", err)
		}

		// Keep metadata fields equal to ignore unintentional patch.
		newObj.SetResourceVersion(obj.GetResourceVersion())
		newObj.SetCreationTimestamp(obj.GetCreationTimestamp())
		newObj.SetUID(obj.GetUID())
		newObj.SetGeneration(obj.GetGeneration())
		newObj.SetManagedFields(obj.GetManagedFields())
		newObj.SetFinalizers(obj.GetFinalizers())

		// Status fields should be omitted.
		delete(obj.Object, "status")
		delete(newObj.Object, "status")

		original, err := obj.MarshalJSON()
		if err != nil {
			return fmt.Errorf("marshaling original resource: %w", err)
		}

		modified, err := newObj.MarshalJSON()
		if err != nil {
			return fmt.Errorf("marshaling modified resource: %w", err)
		}

		patch, err := jsonpatch.CreateMergePatch(original, modified)
		if err != nil {
			return fmt.Errorf("creating patch: %w", err)
		}

		// If resources are identical, patch will be equal '{}'.
		if len(patch) <= 2 {
			log.Info("skipping patch, resources are identical")
			return nil
		}

		log.Infof("patching resource: %s", patch)
		_, err = resource.Patch(ctx, obj.GetName(), k8s_types.MergePatchType, patch, metav1.PatchOptions{})
		if err != nil {
			return fmt.Errorf("patching resource %v: %w", obj.GetName(), err)
		}

		return nil
	}

	return nil
}
