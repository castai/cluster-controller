package actions

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	jsonpatch "github.com/evanphx/json-patch"
	"github.com/sirupsen/logrus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"

	"github.com/castai/cluster-controller/castai"
)

type createHandler struct {
	log    logrus.FieldLogger
	client dynamic.Interface
}

func newCreateHandler(log logrus.FieldLogger, client dynamic.Interface) ActionHandler {
	return &createHandler{
		log:    log,
		client: client,
	}
}

func (h *createHandler) Handle(ctx context.Context, action *castai.ClusterAction) error {
	req, ok := action.Data().(*castai.ActionCreate)
	if !ok {
		return newUnexpectedTypeErr(action.Data(), req)
	}

	if req.Object == nil {
		return errors.New("no object provided")
	}

	newObj := &unstructured.Unstructured{Object: req.Object}
	if newObj.GetNamespace() == "" {
		return errors.New("object namespace is missing")
	}

	log := h.log.WithFields(logrus.Fields{
		actionIDLogField: action.ID,
		"action":         reflect.TypeOf(action.Data()).String(),
		"gvr":            req.GroupVersionResource.String(),
		"name":           newObj.GetName(),
	})

	r := h.client.Resource(schema.GroupVersionResource{
		Group:    req.Group,
		Version:  req.Version,
		Resource: req.Resource,
	}).Namespace(newObj.GetNamespace())

	log.Info("creating new resource")
	_, err := r.Create(ctx, newObj, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("creating resource %v: %w", req.Resource, err)
	}

	if apierrors.IsAlreadyExists(err) {
		log.Info("resource already exists, patching")
		obj, err := r.Get(ctx, newObj.GetName(), metav1.GetOptions{})
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
			return err
		}

		modified, err := newObj.MarshalJSON()
		if err != nil {
			return err
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
		_, err = r.Patch(ctx, obj.GetName(), types.MergePatchType, patch, metav1.PatchOptions{})
		if err != nil {
			return fmt.Errorf("patching resource %v: %w", obj.GetName(), err)
		}

		return nil
	}

	return nil
}
