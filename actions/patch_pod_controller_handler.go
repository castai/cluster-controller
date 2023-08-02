package actions

import (
	"context"
	"fmt"

	"github.com/samber/lo"
	"github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apitypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	"github.com/castai/cluster-controller/castai"
)

type patchPodControllerHandler struct {
	log       logrus.FieldLogger
	clientset kubernetes.Interface
}

func newPatchPodControllerHandler(log logrus.FieldLogger, clientset kubernetes.Interface) ActionHandler {
	return &patchPodControllerHandler{
		log:       log,
		clientset: clientset,
	}
}

func (h *patchPodControllerHandler) Handle(ctx context.Context, action *castai.ClusterAction) error {
	data, ok := action.Data().(*castai.ActionPatchPodController)
	if !ok {
		return fmt.Errorf("unexpected type %T for patch pod controller handler", action.Data())
	}

	log := h.log.WithFields(logrus.Fields{
		"pod_controller_type":      data.PodControllerID.Type,
		"pod_controller_namespace": data.PodControllerID.Namespace,
		"pod_controller_name":      data.PodControllerID.Name,
		"id":                       action.ID,
	})

	isSupported := isSupportedControllerType(data.PodControllerID.Type)
	if !isSupported {
		log.Infof("unsupported controller type, skipping patch action")
		return nil
	}

	deployment, err := h.clientset.AppsV1().
		Deployments(data.PodControllerID.Namespace).
		Get(ctx, data.PodControllerID.Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Infof("controller not found, skipping patch action")
			return nil
		}

		return fmt.Errorf("failed to get deployment %s/%s: %w", data.PodControllerID.Namespace, data.PodControllerID.Name, err)
	}

	if err := patchObject[appsv1.Deployment](ctx, deployment, func(deployment *appsv1.Deployment) error {
		return mergeDiff(log, deployment, data)
	}, func(ctx context.Context, patchType apitypes.PatchType, bytes []byte, options metav1.PatchOptions) error {
		_, err := h.clientset.AppsV1().
			Deployments(data.PodControllerID.Namespace).
			Patch(ctx, data.PodControllerID.Name, patchType, bytes, options)
		return err
	}); err != nil {
		return fmt.Errorf("patching deployment: %w", err)
	}

	return nil
}

const (
	ControllerTypeDeployment = "Deployment"
)

func mergeDiff(log logrus.FieldLogger, deployment *appsv1.Deployment, data *castai.ActionPatchPodController) error {
	for _, container := range data.Containers {
		found, err := mergeContainer(deployment, container)
		if err != nil {
			return fmt.Errorf("failed to merge container %s: %w", container.Name, err)
		}

		if !found {
			log.Infof("container %s not found, skipping", container.Name)
		}
	}

	return nil
}

func mergeContainer(deployment *appsv1.Deployment, container castai.PodContainer) (bool, error) {
	target, i, found := lo.FindIndexOf(deployment.Spec.Template.Spec.Containers, func(c v1.Container) bool {
		return c.Name == container.Name
	})
	if !found {
		return false, nil
	}

	requests, err := mergeActionResourcesIntoResourceList(target.Resources.Requests, container.Requests)
	if err != nil {
		return true, fmt.Errorf("failed to merge requests: %w", err)
	}
	limits, err := mergeActionResourcesIntoResourceList(target.Resources.Limits, container.Limits)
	if err != nil {
		return true, fmt.Errorf("failed to merge limits: %w", err)
	}

	deployment.Spec.Template.Spec.Containers[i].Resources.Requests = requests
	deployment.Spec.Template.Spec.Containers[i].Resources.Limits = limits

	return true, nil
}

func mergeActionResourcesIntoResourceList(original v1.ResourceList, new map[string]string) (v1.ResourceList, error) {
	out := make(v1.ResourceList, len(original))
	for k, v := range original {
		out[k] = v
	}

	for k, v := range new {
		if resourceName, isRemove := asRemoveAction(k); isRemove {
			delete(out, v1.ResourceName(resourceName))
			continue
		}

		value, err := resource.ParseQuantity(v)
		if err != nil {
			return nil, fmt.Errorf("failed to parse resource %s: %w", v, err)
		}

		out[v1.ResourceName(k)] = value
	}

	return out, nil
}

func asRemoveAction(action string) (string, bool) {
	if len(action) > 0 && action[0] == '-' {
		return action[1:], true
	}

	return "", false
}

func isSupportedControllerType(controllerType string) bool {
	return controllerType == ControllerTypeDeployment
}
