package actions

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	policyv1beta1 "k8s.io/api/policy/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/kubectl/pkg/drain"

	"github.com/castai/cluster-controller/internal/castai"
	"github.com/castai/cluster-controller/internal/waitext"
)

func NewEvictPodHandler(log logrus.FieldLogger, clientset kubernetes.Interface) ActionHandler {
	return &EvictPodHandler{
		log:       log,
		clientset: clientset,
	}
}

type EvictPodHandler struct {
	log       logrus.FieldLogger
	clientset kubernetes.Interface
}

func (h *EvictPodHandler) Handle(ctx context.Context, action *castai.ClusterAction) error {
	req, ok := action.Data().(*castai.ActionEvictPod)
	if !ok {
		return newUnexpectedTypeErr(action.Data(), req)
	}
	log := h.log.WithFields(logrus.Fields{
		ActionIDLogField: action.ID,
		"action":         reflect.TypeOf(req).String(),
		"namespace":      req.Namespace,
		"pod":            req.Pod,
	})
	return h.handle(ctx, log, req)
}

func (h *EvictPodHandler) handle(ctx context.Context, log logrus.FieldLogger, req *castai.ActionEvictPod) error {
	log.Infof("evicting pod")
	err := h.evictPod(ctx, log, req.Namespace, req.Pod)
	if err != nil {
		return fmt.Errorf("evict pod: %w", err)
	}
	log.Infof("waiting for pod terminatation")
	err = h.waitForPodToBeDeleted(ctx, log, req.Namespace, req.Pod)
	if err != nil {
		return fmt.Errorf("wait for pod to be terminated: %w", err)
	}
	return nil
}

func (h *EvictPodHandler) evictPod(ctx context.Context, log logrus.FieldLogger, namespace, name string) error {
	groupVersion, err := drain.CheckEvictionSupport(h.clientset)
	if err != nil {
		return fmt.Errorf("checking eviction support: %w", err)
	}
	var submit func(context.Context) error
	switch groupVersion {
	case schema.GroupVersion{}:
		return errors.New("eviction not supported")
	case policyv1beta1.SchemeGroupVersion:
		submit = func(ctx context.Context) error {
			log.Debugf("submitting policy/v1beta1 eviction request")
			return h.clientset.CoreV1().Pods(namespace).EvictV1beta1(ctx, &policyv1beta1.Eviction{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      name,
				},
			})
		}
	case policyv1.SchemeGroupVersion:
		submit = func(ctx context.Context) error {
			log.Debugf("submitting policy/v1 eviction request")
			return h.clientset.CoreV1().Pods(namespace).EvictV1(ctx, &policyv1.Eviction{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: namespace,
					Name:      name,
				},
			})
		}
	default:
		return fmt.Errorf("unsupported eviction version: %s", groupVersion.String())
	}

	return waitext.Retry(
		ctx,
		defaultBackoff(),
		waitext.Forever,
		func(ctx context.Context) (bool, error) {
			err := submit(ctx)
			if err != nil {
				if apierrors.IsNotFound(err) {
					// We wanted this pod gone anyway.
					return false, nil
				}
				if apierrors.IsInternalError(err) {
					// We expect this to likely be some kind of misconfiguration therefore not retrying.
					return false, err
				}
				return true, err
			}
			return false, nil
		},
		func(err error) {
			log.Warnf("will retry submitting eviction requests: %v", err)
		},
	)
}

func (h *EvictPodHandler) waitForPodToBeDeleted(ctx context.Context, log logrus.FieldLogger, namespace, name string) error {
	return waitext.Retry(
		ctx, // controls how long we might wait at most.
		defaultBackoff(),
		waitext.Forever,
		func(ctx context.Context) (bool, error) {
			deleted, phase, err := h.isPodDeleted(ctx, namespace, name)
			if err != nil {
				return true, err
			}
			if deleted {
				return false, nil
			}
			return true, fmt.Errorf("pod is in phase %s", phase)
		},
		func(err error) {
			log.Warnf("will retry checking pod status: %v", err)
		},
	)
}

func (h *EvictPodHandler) isPodDeleted(ctx context.Context, namespace, name string) (bool, v1.PodPhase, error) {
	p, err := h.clientset.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		return true, "", nil // Already gone.
	}
	if err != nil {
		return false, "", err
	}
	if p.Status.Phase == v1.PodSucceeded || p.Status.Phase == v1.PodFailed {
		return true, "", nil
	}
	return false, p.Status.Phase, nil
}
