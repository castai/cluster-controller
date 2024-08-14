package actions

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/samber/lo"
	"github.com/sirupsen/logrus"
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
	"github.com/castai/cluster-controller/waitext"
)

const (
	minDrainTimeout = 0                // Minimal pod drain timeout
	roundTripTime   = 10 * time.Second // 2xPollInterval for action
)

type drainNodeConfig struct {
	podsDeleteTimeout             time.Duration
	podDeleteRetries              int
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
	requestedTimeout := time.Duration(action.ActionDrainNode.DrainTimeoutSeconds) * time.Second

	drainTimeout := requestedTimeout - timeSinceCreated

	return lo.Clamp(drainTimeout, minDrainTimeout*time.Second, requestedTimeout)
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
		"node_name":      req.NodeName,
		"node_id":        req.NodeID,
		"action":         reflect.TypeOf(action.Data().(*castai.ActionDrainNode)).String(),
		actionIDLogField: action.ID,
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

	// If we reached timeout (maybe a pod was stuck in eviction or we hit another hiccup) and we are forced to drain, then delete pods
	// This skips eviction API and even if pods have PDBs (for example), the pods are still deleted.
	// If the node is going away (like spot interruption), there is no point in trying to keep the pods alive.
	if errors.Is(err, context.DeadlineExceeded) {
		if !req.Force {
			return err
		}

		h.log.Infof("timeout=%f exceeded during pod eviction, force=%v, starting pod deletion", drainTimeout.Seconds(), req.Force)
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

	err := patchNode(ctx, h.log, h.clientset, node, func(n *v1.Node) {
		n.Spec.Unschedulable = true
	})
	if err != nil {
		return fmt.Errorf("patching node unschedulable: %w", err)
	}
	return nil
}

func (h *drainNodeHandler) evictNodePods(ctx context.Context, log logrus.FieldLogger, node *v1.Node) error {
	pods, err := h.listNodePodsToEvict(ctx, log, node)
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

	return h.waitNodePodsTerminated(ctx, log, node)
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

	return h.waitNodePodsTerminated(ctx, log, node)
}

func (h *drainNodeHandler) sendPodsRequests(ctx context.Context, pods []v1.Pod, f func(context.Context, v1.Pod) error) error {
	if len(pods) == 0 {
		return nil
	}

	var (
		parallelTasks = int(lo.Clamp(float64(len(pods)), 30, 100))
		taskChan      = make(chan v1.Pod, len(pods))
		taskErrs      = make([]error, 0)
		taskErrsMx    sync.Mutex
		wg            sync.WaitGroup
	)

	h.log.Debugf("Starting %d parallel tasks for %d pods: [%v]", parallelTasks, len(pods), lo.Map(pods, func(t v1.Pod, i int) string {
		return fmt.Sprintf("%s/%s", t.Namespace, t.Name)
	}))

	worker := func(taskChan <-chan v1.Pod) {
		for pod := range taskChan {
			if err := f(ctx, pod); err != nil {
				taskErrsMx.Lock()
				taskErrs = append(taskErrs, fmt.Errorf("pod %s/%s failed operation: %w", pod.Namespace, pod.Name, err))
				taskErrsMx.Unlock()
			}
		}
		wg.Done()
	}

	for range parallelTasks {
		wg.Add(1)
		go worker(taskChan)
	}

	for _, pod := range pods {
		taskChan <- pod
	}

	close(taskChan)
	wg.Wait()

	return errors.Join(taskErrs...)
}

func (h *drainNodeHandler) listNodePodsToEvict(ctx context.Context, log logrus.FieldLogger, node *v1.Node) ([]v1.Pod, error) {
	var pods *v1.PodList
	err := waitext.Retry(
		ctx,
		defaultBackoff(),
		defaultMaxRetriesK8SOperation,
		func(ctx context.Context) (bool, error) {
			p, err := h.clientset.CoreV1().Pods(metav1.NamespaceAll).List(ctx, metav1.ListOptions{
				FieldSelector: fields.SelectorFromSet(fields.Set{"spec.nodeName": node.Name}).String(),
			})
			if err != nil {
				return true, err
			}
			pods = p
			return false, nil
		},
		func(err error) {
			log.Warnf("listing pods on node %s: %v", node.Name, err)
		},
	)
	if err != nil {
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
			continue
		}

		if !isDaemonSetPod(&p) && !isStaticPod(&p) {
			podsToEvict = append(podsToEvict, p)
		}
	}

	logCastPodsToEvict(log, castPods)
	podsToEvict = append(podsToEvict, castPods...)
	return podsToEvict, nil
}

func (h *drainNodeHandler) waitNodePodsTerminated(ctx context.Context, log logrus.FieldLogger, node *v1.Node) error {
	return waitext.Retry(
		ctx,
		waitext.NewConstantBackoff(h.cfg.podsTerminationWaitRetryDelay),
		waitext.Forever,
		func(ctx context.Context) (bool, error) {
			pods, err := h.listNodePodsToEvict(ctx, log, node)
			if err != nil {
				return true, fmt.Errorf("listing %q pods to be terminated: %w", node.Name, err)
			}
			if len(pods) > 0 {
				return true, fmt.Errorf("waiting for %d pods to be terminated on node %v", len(pods), node.Name)
			}
			return false, nil
		},
		func(err error) {
			h.log.Warnf("waiting for pod termination on node %v, will retry: %v", node.Name, err)
		},
	)
}

// evictPod from the k8s node. Error handling is based on eviction api documentation:
// https://kubernetes.io/docs/tasks/administer-cluster/safely-drain-node/#the-eviction-api
func (h *drainNodeHandler) evictPod(ctx context.Context, pod v1.Pod, groupVersion schema.GroupVersion) error {
	b := waitext.NewConstantBackoff(h.cfg.podEvictRetryDelay)
	action := func(ctx context.Context) (bool, error) {
		var err error

		h.log.Debugf("requesting eviction for pod %s/%s", pod.Namespace, pod.Name)
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
				return false, nil
			}

			// Pod is misconfigured - stop retry.
			if apierrors.IsInternalError(err) {
				return false, err
			}

			// If PDB is violated, K8S returns 429 TooManyRequests with specific cause
			// TODO: With KUBE-479, rethink this flow in general
			if apierrors.IsTooManyRequests(err) && apierrors.HasStatusCause(err, policyv1.DisruptionBudgetCause) {
				return true, err
			}
		}

		// Other errors - retry.
		return true, err
	}
	err := waitext.Retry(ctx, b, waitext.Forever, action, func(err error) {
		h.log.Warnf("evict pod %s on node %s in namespace %s, will retry: %v", pod.Name, pod.Spec.NodeName, pod.Namespace, err)
	})
	if err != nil {
		return fmt.Errorf("evicting pod %s in namespace %s: %w", pod.Name, pod.Namespace, err)

	}
	return nil
}

func (h *drainNodeHandler) deletePod(ctx context.Context, options metav1.DeleteOptions, pod v1.Pod) error {
	b := waitext.NewConstantBackoff(h.cfg.podDeleteRetryDelay)
	action := func(ctx context.Context) (bool, error) {
		err := h.clientset.CoreV1().Pods(pod.Namespace).Delete(ctx, pod.Name, options)
		if err != nil {
			// Pod is not found - ignore.
			if apierrors.IsNotFound(err) {
				return false, nil
			}

			// Pod is misconfigured - stop retry.
			if apierrors.IsInternalError(err) {
				return false, err
			}
		}

		// Other errors - retry.
		return true, err
	}
	err := waitext.Retry(ctx, b, h.cfg.podDeleteRetries, action, func(err error) {
		h.log.Warnf("deleting pod %s on node %s in namespace %s, will retry: %v", pod.Name, pod.Spec.NodeName, pod.Namespace, err)
	})
	if err != nil {
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
