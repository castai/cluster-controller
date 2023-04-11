package actions

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/samber/lo"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
	v1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/api/policy/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/kubectl/pkg/drain"

	"github.com/castai/cluster-controller/castai"
)

const (
	minDrainTimeout = 0                // Minimal pod drain timeout
	roundTripTime   = 10 * time.Second // 2xPollInterval for action
)

type drainNodeConfig struct {
	podsDeleteTimeout             time.Duration
	podDeleteRetries              uint64
	podDeleteRetryDelay           time.Duration
	podEvictRetryDelay            time.Duration
	podsTerminationWaitRetryDelay time.Duration
	castNamespace                 string
	skipDeletedTimeoutSeconds     int
}

func newDrainNodeHandler(log logrus.FieldLogger, clientset kubernetes.Interface, castNamespace string) ActionHandler {
	return &drainNodeHandler{
		log:       log,
		clientset: clientset,
		cfg: drainNodeConfig{
			podsDeleteTimeout:             2 * time.Minute,
			podDeleteRetries:              5,
			podDeleteRetryDelay:           5 * time.Second,
			podEvictRetryDelay:            5 * time.Second,
			podsTerminationWaitRetryDelay: 10 * time.Second,
			castNamespace:                 castNamespace,
			skipDeletedTimeoutSeconds:     60,
		},
	}
}

// getDrainTimeout returns drain timeout adjusted to action creation time.
// the result is clamped between 0s and the requested timeout.
func (h *drainNodeHandler) getDrainTimeout(action *castai.ClusterAction) time.Duration {
	timeSinceCreated := time.Since(action.CreatedAt)
	drainTimeout := time.Duration(action.ActionDrainNode.DrainTimeoutSeconds) * time.Second

	// Remove 2 poll interval required for polling the action.
	timeSinceCreated = timeSinceCreated - roundTripTime

	return lo.Clamp(drainTimeout-timeSinceCreated, minDrainTimeout*time.Second, time.Duration(action.ActionDrainNode.DrainTimeoutSeconds)*time.Second)
}

type drainNodeHandler struct {
	log       logrus.FieldLogger
	clientset kubernetes.Interface
	cfg       drainNodeConfig
}

func (h *drainNodeHandler) Handle(ctx context.Context, action *castai.ClusterAction) error {
	req, ok := action.Data().(*castai.ActionDrainNode)
	if !ok {
		return fmt.Errorf("unexpected type %T for drain handler", action.Data())
	}
	drainTimeout := h.getDrainTimeout(action)
	log := h.log.WithFields(logrus.Fields{
		"node_name": req.NodeName,
		"node_id":   req.NodeID,
		"action":    reflect.TypeOf(action.Data().(*castai.ActionDrainNode)).String(),
		"id":        action.ID,
	})

	node, err := h.clientset.CoreV1().Nodes().Get(ctx, req.NodeName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("node not found, skipping draining")
			return nil
		}
		return err
	}

	log.Infof("draining node, drain_timeout_seconds=%f, force=%v created_at=%s", drainTimeout.Seconds(), req.Force, action.CreatedAt)

	if err := h.taintNode(ctx, node); err != nil {
		return fmt.Errorf("tainting node %q: %w", req.NodeName, err)
	}

	// First try to evict pods gracefully.
	evictCtx, evictCancel := context.WithTimeout(ctx, drainTimeout)
	defer evictCancel()
	err = h.evictNodePods(evictCtx, log, node)
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("eviciting node pods: %w", err)
	}

	if errors.Is(err, context.DeadlineExceeded) {
		if !req.Force {
			return err
		}
		// Try deleting pods gracefully first, then delete with 0 grace period.
		options := []metav1.DeleteOptions{
			{},
			*metav1.NewDeleteOptions(0),
		}

		var err error
		for _, o := range options {
			deleteCtx, deleteCancel := context.WithTimeout(ctx, h.cfg.podsDeleteTimeout)
			defer deleteCancel()

			err = h.deleteNodePods(deleteCtx, log, node, o)
			if err == nil {
				break
			}
			if !errors.Is(err, context.DeadlineExceeded) {
				return fmt.Errorf("forcefully deleting pods: %w", err)
			}
		}

		return err
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
	pods, err := h.listNodePodsToEvict(ctx, nil, node)
	if err != nil {
		return err
	}

	log.Infof("evicting %d pods", len(pods))
	if len(pods) == 0 {
		return nil
	}
	groupVersion, err := drain.CheckEvictionSupport(h.clientset)
	if err != nil {
		return err
	}
	evictPod := func(ctx context.Context, pod v1.Pod) error {
		return h.evictPod(ctx, pod, groupVersion)
	}

	if err := h.sendPodsRequests(ctx, pods, evictPod); err != nil {
		return fmt.Errorf("sending evict pods requests: %w", err)
	}

	return h.waitNodePodsTerminated(ctx, node)
}

func (h *drainNodeHandler) deleteNodePods(ctx context.Context, log logrus.FieldLogger, node *v1.Node, options metav1.DeleteOptions) error {
	pods, err := h.listNodePodsToEvict(ctx, log, node)
	if err != nil {
		return err
	}

	if options.GracePeriodSeconds != nil {
		log.Infof("forcefully deleting %d pods with gracePeriod %d", len(pods), *options.GracePeriodSeconds)
	} else {
		log.Infof("forcefully deleting %d pods", len(pods))
	}

	if len(pods) == 0 {
		return nil
	}

	deletePod := func(ctx context.Context, pod v1.Pod) error {
		return h.deletePod(ctx, options, pod)
	}

	if err := h.sendPodsRequests(ctx, pods, deletePod); err != nil {
		return fmt.Errorf("sending delete pods requests: %w", err)
	}

	return h.waitNodePodsTerminated(ctx, node)
}

func (h *drainNodeHandler) sendPodsRequests(ctx context.Context, pods []v1.Pod, f func(context.Context, v1.Pod) error) error {
	batchSize := lo.Clamp(0.2*float64(len(pods)), 5, 20)
	for _, batch := range lo.Chunk(pods, int(batchSize)) {
		g, ctx := errgroup.WithContext(ctx)
		for _, pod := range batch {
			pod := pod
			g.Go(func() error { return f(ctx, pod) })
		}
		if err := g.Wait(); err != nil {
			return err
		}
	}

	return nil
}

func (h *drainNodeHandler) listNodePodsToEvict(ctx context.Context, log logrus.FieldLogger, node *v1.Node) ([]v1.Pod, error) {
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

	podsToEvict := make([]v1.Pod, 0)
	castPods := make([]v1.Pod, 0)
	// Evict CAST PODs as last ones
	for _, p := range pods.Items {
		// Skip pods that have been recently removed.
		if !p.ObjectMeta.DeletionTimestamp.IsZero() &&
			int(time.Since(p.ObjectMeta.GetDeletionTimestamp().Time).Seconds()) > h.cfg.skipDeletedTimeoutSeconds {
			continue
		}

		// Skip completed pods. Will be removed during node removal.
		if p.Status.Phase == v1.PodSucceeded {
			continue
		}

		if p.Namespace == h.cfg.castNamespace && !isDaemonSetPod(&p) && !isStaticPod(&p) {
			castPods = append(castPods, p)
		}

		if !isDaemonSetPod(&p) && !isStaticPod(&p) {
			podsToEvict = append(podsToEvict, p)
		}
	}

	logCastPodsToEvict(log, castPods)
	podsToEvict = append(podsToEvict, castPods...)
	return podsToEvict, nil
}

func (h *drainNodeHandler) waitNodePodsTerminated(ctx context.Context, node *v1.Node) error {
	return backoff.Retry(func() error {
		pods, err := h.listNodePodsToEvict(ctx, nil, node)
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
func (h *drainNodeHandler) evictPod(ctx context.Context, pod v1.Pod, groupVersion schema.GroupVersion) error {
	b := backoff.WithContext(backoff.NewConstantBackOff(h.cfg.podEvictRetryDelay), ctx) // nolint:gomnd
	action := func() error {
		var err error
		if groupVersion == policyv1.SchemeGroupVersion {
			err = h.clientset.PolicyV1().Evictions(pod.Namespace).Evict(ctx, &policyv1.Eviction{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pod.Name,
					Namespace: pod.Namespace,
				},
			})
		} else {
			err = h.clientset.CoreV1().Pods(pod.Namespace).EvictV1beta1(ctx, &v1beta1.Eviction{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "policy/v1beta1",
					Kind:       "Eviction",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      pod.Name,
					Namespace: pod.Namespace,
				},
			})
		}

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

func logCastPodsToEvict(log logrus.FieldLogger, castPods []v1.Pod) {
	if len(castPods) == 0 {
		return
	}

	castPodsNames := make([]string, 0, len(castPods))
	for _, p := range castPods {
		castPodsNames = append(castPodsNames, p.Name)
	}
	joinedPodNames := strings.Join(castPodsNames, ", ")

	log.Warnf("evicting CAST AI pods: %s", joinedPodNames)
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
