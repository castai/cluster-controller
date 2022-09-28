package actions

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/samber/lo"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
	v1 "k8s.io/api/core/v1"
	"k8s.io/api/policy/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"

	"github.com/castai/cluster-controller/castai"
)

type drainNodeConfig struct {
	podsDeleteTimeout             time.Duration
	podDeleteRetries              uint64
	podDeleteRetryDelay           time.Duration
	podEvictRetryDelay            time.Duration
	podsTerminationWaitRetryDelay time.Duration
}

func newDrainNodeHandler(log logrus.FieldLogger, clientset kubernetes.Interface) ActionHandler {
	return &drainNodeHandler{
		log:       log,
		clientset: clientset,
		cfg: drainNodeConfig{
			podsDeleteTimeout:             2 * time.Minute,
			podDeleteRetries:              5,
			podDeleteRetryDelay:           5 * time.Second,
			podEvictRetryDelay:            5 * time.Second,
			podsTerminationWaitRetryDelay: 10 * time.Second,
		},
	}
}

type drainNodeHandler struct {
	log       logrus.FieldLogger
	clientset kubernetes.Interface
	cfg       drainNodeConfig
}

func (h *drainNodeHandler) Handle(ctx context.Context, data interface{}) error {
	req, ok := data.(*castai.ActionDrainNode)
	if !ok {
		return fmt.Errorf("unexpected type %T for drain handler", data)
	}

	log := h.log.WithFields(logrus.Fields{"node_name": req.NodeName, "action": "drain"})

	node, err := h.clientset.CoreV1().Nodes().Get(ctx, req.NodeName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("node not found, skipping draining")
			return nil
		}
		return err
	}

	log.Infof("draining node, drain_timeout_seconds=%d, force=%v", req.DrainTimeoutSeconds, req.Force)

	if err := h.taintNode(ctx, node); err != nil {
		return fmt.Errorf("tainting node %q: %w", req.NodeName, err)
	}

	// First try to evict pods gracefully.
	evictCtx, evictCancel := context.WithTimeout(ctx, time.Duration(req.DrainTimeoutSeconds)*time.Second)
	defer evictCancel()
	err = h.evictNodePods(evictCtx, log, node)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("eviciting node pods: %w", err)
	}

	if errors.Is(err, context.DeadlineExceeded) {
		if !req.Force {
			return err
		}
		// If force is set and evict timeout exceeded delete pods.
		deleteCtx, deleteCancel := context.WithTimeout(ctx, h.cfg.podsDeleteTimeout)
		defer deleteCancel()
		err = h.deleteNodePods(deleteCtx, log, node, metav1.DeleteOptions{})
		if err == nil {
			log.Info("node drained")
			return nil
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("forcefully deleting pods: %w", err)
		}

		deleteForceCtx, deleteForceCancel := context.WithTimeout(ctx, h.cfg.podsDeleteTimeout)
		defer deleteForceCancel()
		if err = h.deleteNodePods(deleteForceCtx, log, node, *metav1.NewDeleteOptions(0)); err != nil {
			return fmt.Errorf("deleting gracePeriod=0 pods: %w", err)
		}
	}

	log.Info("node drained")

	return nil
}

func (h *drainNodeHandler) taintNode(ctx context.Context, node *v1.Node) error {
	if node.Spec.Unschedulable {
		return nil
	}

	err := patchNode(ctx, h.clientset, node, func(n *v1.Node) error {
		n.Spec.Unschedulable = true
		return nil
	})
	if err != nil {
		return fmt.Errorf("patching node unschedulable: %w", err)
	}
	return nil
}

func (h *drainNodeHandler) evictNodePods(ctx context.Context, log logrus.FieldLogger, node *v1.Node) error {
	pods, err := h.listNodePodsToEvict(ctx, node)
	if err != nil {
		return err
	}

	log.Infof("evicting %d pods", len(pods))

	if err := h.sendPodsRequests(ctx, metav1.DeleteOptions{}, pods, h.evictPod); err != nil {
		return fmt.Errorf("sending evict pods requests: %w", err)
	}

	return h.waitNodePodsTerminated(ctx, node)
}

func (h *drainNodeHandler) deleteNodePods(ctx context.Context, log logrus.FieldLogger, node *v1.Node, options metav1.DeleteOptions) error {
	pods, err := h.listNodePodsToEvict(ctx, node)
	if err != nil {
		return err
	}

	if options.GracePeriodSeconds != nil {
		log.Infof("forcefully deleting %d pods with gracePeriod %d", len(pods), *options.GracePeriodSeconds)
	} else {
		log.Infof("forcefully deleting %d pods", len(pods))
	}

	if err := h.sendPodsRequests(ctx, options, pods, h.deletePod); err != nil {
		return fmt.Errorf("sending delete pods requests: %w", err)
	}

	return h.waitNodePodsTerminated(ctx, node)
}

func (h *drainNodeHandler) sendPodsRequests(ctx context.Context, options metav1.DeleteOptions, pods []v1.Pod, f func(context.Context, metav1.DeleteOptions, v1.Pod) error) error {
	const batchSize = 5

	for _, batch := range lo.Chunk(pods, batchSize) {
		g, ctx := errgroup.WithContext(ctx)
		for _, pod := range batch {
			pod := pod
			g.Go(func() error { return f(ctx, options, pod) })
		}
		if err := g.Wait(); err != nil {
			return err
		}
	}

	return nil
}

func (h *drainNodeHandler) listNodePodsToEvict(ctx context.Context, node *v1.Node) ([]v1.Pod, error) {
	var pods *v1.PodList
	if err := backoff.Retry(func() error {
		p, err := h.clientset.CoreV1().Pods(metav1.NamespaceAll).List(ctx, metav1.ListOptions{
			FieldSelector: fields.SelectorFromSet(fields.Set{"spec.nodeName": node.Name}).String(),
		})
		if err != nil {
			return err
		}
		pods = p
		return nil
	}, defaultBackoff(ctx)); err != nil {
		return nil, fmt.Errorf("listing node %v pods: %w", node.Name, err)
	}

	podsToEvict := lo.Filter(pods.Items, func(pod v1.Pod, _ int) bool {
		return !isDaemonSetPod(&pod) && !isStaticPod(&pod)
	})

	return podsToEvict, nil
}

func (h *drainNodeHandler) waitNodePodsTerminated(ctx context.Context, node *v1.Node) error {
	return backoff.Retry(func() error {
		pods, err := h.listNodePodsToEvict(ctx, node)
		if err != nil {
			return fmt.Errorf("waiting for node %q pods to be terminated: %w", node.Name, err)
		}
		if len(pods) > 0 {
			return fmt.Errorf("waiting for %d pods to be terminated on node %v", len(pods), node.Name)
		}
		return nil
	}, backoff.WithContext(backoff.NewConstantBackOff(h.cfg.podsTerminationWaitRetryDelay), ctx))
}

// evictPod from the k8s node. Error handling is based on eviction api documentation:
// https://kubernetes.io/docs/tasks/administer-cluster/safely-drain-node/#the-eviction-api
func (h *drainNodeHandler) evictPod(ctx context.Context, options metav1.DeleteOptions, pod v1.Pod) error {
	b := backoff.WithContext(backoff.NewConstantBackOff(h.cfg.podEvictRetryDelay), ctx) // nolint:gomnd
	action := func() error {
		err := h.clientset.CoreV1().Pods(pod.Namespace).Evict(ctx, &v1beta1.Eviction{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "policy/v1beta1",
				Kind:       "Eviction",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      pod.Name,
				Namespace: pod.Namespace,
			},
			DeleteOptions: &options,
		})

		if err != nil {
			// Pod is not found - ignore.
			if apierrors.IsNotFound(err) {
				return nil
			}

			// Pod is misconfigured - stop retry.
			if apierrors.IsInternalError(err) {
				return backoff.Permanent(err)
			}
		}

		// Other errors - retry.
		return err
	}
	if err := backoff.Retry(action, b); err != nil {
		return fmt.Errorf("evicting pod %s in namespace %s: %w", pod.Name, pod.Namespace, err)
	}
	return nil
}

func (h *drainNodeHandler) deletePod(ctx context.Context, options metav1.DeleteOptions, pod v1.Pod) error {
	b := backoff.WithContext(backoff.WithMaxRetries(backoff.NewConstantBackOff(h.cfg.podDeleteRetryDelay), h.cfg.podDeleteRetries), ctx) // nolint:gomnd
	action := func() error {
		err := h.clientset.CoreV1().Pods(pod.Namespace).Delete(ctx, pod.Name, options)
		if err != nil {
			// Pod is not found - ignore.
			if apierrors.IsNotFound(err) {
				return nil
			}

			// Pod is misconfigured - stop retry.
			if apierrors.IsInternalError(err) {
				return backoff.Permanent(err)
			}
		}

		// Other errors - retry.
		return err
	}
	if err := backoff.Retry(action, b); err != nil {
		return fmt.Errorf("deleting pod %s in namespace %s: %w", pod.Name, pod.Namespace, err)
	}
	return nil
}

func isDaemonSetPod(p *v1.Pod) bool {
	return isControlledBy(p, "DaemonSet")
}

func isStaticPod(p *v1.Pod) bool {
	return isControlledBy(p, "Node")
}

func isControlledBy(p *v1.Pod, kind string) bool {
	ctrl := metav1.GetControllerOf(p)

	return ctrl != nil && ctrl.Kind == kind
}
