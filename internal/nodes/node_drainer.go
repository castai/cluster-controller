//go:generate mockgen -destination ./mock/kubernetes.go . Drainer

package nodes

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/samber/lo"
	"github.com/sirupsen/logrus"
	core "k8s.io/api/core/v1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/kubectl/pkg/drain"

	"github.com/castai/cluster-controller/internal/informer"
	"github.com/castai/cluster-controller/internal/k8s"
	"github.com/castai/cluster-controller/internal/logger"
	"github.com/castai/cluster-controller/internal/waitext"
)

type EvictRequest struct {
	Node                      string
	CastNamespace             string
	SkipDeletedTimeoutSeconds int
}

type DrainRequest struct {
	Node                      string
	CastNamespace             string
	SkipDeletedTimeoutSeconds int
	DeleteOptions             meta.DeleteOptions
}

type Drainer interface {
	Evict(ctx context.Context, data EvictRequest) ([]*core.Pod, error)
	Drain(ctx context.Context, data DrainRequest) ([]*core.Pod, error)
}

type DrainerConfig struct {
	PodEvictRetryDelay            time.Duration
	PodsTerminationWaitRetryDelay time.Duration
	PodDeleteRetries              int
}

type drainer struct {
	pods   informer.PodInformer
	client k8s.Client
	cfg    DrainerConfig
	log    logrus.FieldLogger
}

func NewDrainer(
	pods informer.PodInformer,
	client k8s.Client,
	log logrus.FieldLogger,
	cfg DrainerConfig,
) Drainer {
	m := &drainer{
		pods:   pods,
		client: client,
		cfg:    cfg,
		log:    log,
	}
	return m
}

func (d *drainer) Drain(ctx context.Context, data DrainRequest) ([]*core.Pod, error) {
	tryFn := func(ctx context.Context, pods []*core.Pod) ([]*core.Pod, []*core.Pod, error) {
		return d.tryDrain(ctx, pods, data.DeleteOptions)
	}
	retryFn := func(ctx context.Context, pod *core.Pod) error {
		return d.client.DeletePod(ctx, data.DeleteOptions, *pod, d.cfg.PodDeleteRetries, d.cfg.PodEvictRetryDelay)
	}
	return d.execute(ctx, data.Node, data.CastNamespace, "drain", data.SkipDeletedTimeoutSeconds, tryFn, retryFn)
}

func (d *drainer) tryDrain(ctx context.Context, toEvict []*core.Pod, options meta.DeleteOptions) ([]*core.Pod, []*core.Pod, error) {
	deletePod := func(ctx context.Context, pod core.Pod) error {
		return d.client.DeletePod(ctx, options, pod, d.cfg.PodDeleteRetries, d.cfg.PodEvictRetryDelay)
	}

	successful, podsWithFailedAction := d.client.ExecuteBatchPodActions(ctx, toEvict, deletePod, "delete-pod")
	failed, err := d.handleFailures(ctx, "deletion", podsWithFailedAction)
	return successful, failed, err
}

func (d *drainer) Evict(ctx context.Context, data EvictRequest) ([]*core.Pod, error) {
	groupVersion, err := drain.CheckEvictionSupport(d.client.Clientset())
	if err != nil {
		return nil, err
	}
	tryFn := func(ctx context.Context, pods []*core.Pod) ([]*core.Pod, []*core.Pod, error) {
		return d.tryEvict(ctx, pods, groupVersion)
	}
	retryFn := func(ctx context.Context, pod *core.Pod) error {
		return d.client.EvictPod(ctx, *pod, d.cfg.PodEvictRetryDelay, groupVersion)
	}
	return d.execute(ctx, data.Node, data.CastNamespace, "eviction", data.SkipDeletedTimeoutSeconds, tryFn, retryFn)
}

func (d *drainer) tryEvict(ctx context.Context, toEvict []*core.Pod, groupVersion schema.GroupVersion) ([]*core.Pod, []*core.Pod, error) {
	evictPod := func(ctx context.Context, pod core.Pod) error {
		return d.client.EvictPod(ctx, pod, d.cfg.PodEvictRetryDelay, groupVersion)
	}

	successful, podsWithFailedAction := d.client.ExecuteBatchPodActions(ctx, toEvict, evictPod, "evict-pod")
	failed, err := d.handleFailures(ctx, "eviction", podsWithFailedAction)
	return successful, failed, err
}

func (d *drainer) execute(
	ctx context.Context,
	node, castNamespace, actionName string,
	skipDeletedTimeoutSeconds int,
	tryFn func(ctx context.Context, pods []*core.Pod) ([]*core.Pod, []*core.Pod, error),
	retryFn func(ctx context.Context, pod *core.Pod) error,
) ([]*core.Pod, error) {
	logger := logger.FromContext(ctx, d.log)
	logger.Infof("starting %s", actionName)

	pods, err := d.list(ctx, node)
	if err != nil {
		return nil, err
	}

	toEvict, toIgnore := d.prioritizePods(pods, castNamespace, skipDeletedTimeoutSeconds)
	if len(toEvict) == 0 {
		return nil, nil
	}

	successful, failed, err := tryFn(ctx, toEvict)
	if err != nil && !errors.Is(err, &k8s.PodFailedActionError{}) {
		return nil, err
	}

	ignored := make([]*core.Pod, len(toIgnore)+len(failed))
	copy(ignored, toIgnore)
	copy(ignored[len(toIgnore):], failed)

	if err = d.waitTerminaition(ctx, node, successful, ignored, retryFn); err != nil {
		return nil, err
	}

	logger.Infof("%s finished", actionName)
	return ignored, nil
}

func (d *drainer) list(_ context.Context, fromNode string) ([]*core.Pod, error) {
	podPtrs, err := d.pods.ListByNode(fromNode)
	if err != nil {
		return nil, fmt.Errorf("listing pods from cache: %w", err)
	}

	pods := make([]*core.Pod, 0, len(podPtrs))
	pods = append(pods, podPtrs...)
	return pods, nil
}

func (d *drainer) prioritizePods(pods []*core.Pod, castNamespace string, skipDeletedTimeoutSeconds int) ([]*core.Pod, []*core.Pod) {
	partitioned := k8s.PartitionPodsForEviction(pods, castNamespace, skipDeletedTimeoutSeconds)

	if len(partitioned.CastPods) == 0 && len(partitioned.Evictable) == 0 {
		return nil, nil
	}

	return partitioned.Evictable, partitioned.NonEvictable
}

func (d *drainer) handleFailures(ctx context.Context, action string, failures []k8s.PodActionFailure) ([]*core.Pod, error) {
	logger := logger.FromContext(ctx, d.log)

	if len(failures) == 0 {
		return nil, nil
	}

	podErrors := lo.Map(failures, func(failure k8s.PodActionFailure, _ int) error {
		return fmt.Errorf("pod %s/%s failed %s: %w", failure.Pod.Namespace, failure.Pod.Name, action, failure.Err)
	})
	failedPodsError := &k8s.PodFailedActionError{
		Action: action,
		Errors: podErrors,
	}
	logger.Warnf("some pods failed %s, will ignore for termination wait: %v", action, failedPodsError)

	failed := lo.Map(failures, func(failure k8s.PodActionFailure, _ int) *core.Pod {
		return failure.Pod
	})

	return failed, failedPodsError
}

func (d *drainer) waitTerminaition(ctx context.Context, fromNode string, successful, ignored []*core.Pod, retryFn func(ctx context.Context, pod *core.Pod) error) error {
	logger := logger.FromContext(ctx, d.log)
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	ignoredSet := podKeySet(ignored)
	successfulSet := podKeySet(successful)

	logger.Infof("starting wait for pod termination from informer")
	logger.Infof("ignored: %v", ignoredSet)
	return waitext.Retry(
		ctx,
		waitext.NewConstantBackoff(d.cfg.PodsTerminationWaitRetryDelay),
		waitext.Forever,
		func(ctx context.Context) (bool, error) {
			return d.checkTermination(ctx, fromNode, ignoredSet, successfulSet, retryFn)
		},
		func(err error) {
			logger.Warnf("waiting for pod termination on node %v, will retry: %v", fromNode, err)
		},
	)
}

func (d *drainer) checkTermination(
	ctx context.Context,
	fromNode string,
	ignored sets.Set[string],
	successful sets.Set[string],
	retryFn func(ctx context.Context, pod *core.Pod) error,
) (bool, error) {
	pods, err := d.list(ctx, fromNode)
	if err != nil {
		return true, fmt.Errorf("listing %q pods to be terminated: %w", fromNode, err)
	}

	remaining := filterRemaining(pods, ignored)

	d.retryStale(ctx, remaining, successful, ignored, retryFn)

	if remainingCount := len(remaining); remainingCount > 0 {
		remainingNames := lo.Map(remaining, func(p *core.Pod, _ int) string { return podKey(p) })
		return true, fmt.Errorf("waiting for %d pods (%v) to be terminated on node %v", remainingCount, remainingNames, fromNode)
	}
	return false, nil
}

func (d *drainer) retryStale(ctx context.Context, pods []*core.Pod, successful, ignored sets.Set[string], retryFn func(ctx context.Context, pod *core.Pod) error) {
	logger := logger.FromContext(ctx, d.log)

	for _, pod := range pods {
		key := podKey(pod)
		if !successful.Has(key) {
			logger.Warnf("pod eviction was not initiated: %v", key)
			if retryFn == nil {
				continue
			}
			if k8s.IsNonEvictible(pod) {
				ignored.Insert(key)
				continue
			}
			if err := retryFn(ctx, pod); err != nil {
				logger.Warnf("re-eviction of stale pod %s failed: %v, will ignore pod", key, err)
				ignored.Insert(key)
			} else {
				successful.Insert(key)
				logger.Infof("re-eviction triggered for stale pod %s", key)
			}
		}
	}
}

func filterRemaining(pods []*core.Pod, ignored sets.Set[string]) []*core.Pod {
	var remaining []*core.Pod
	for _, pod := range pods {
		if !ignored.Has(podKey(pod)) {
			remaining = append(remaining, pod)
		}
	}
	return remaining
}

func podKey(pod *core.Pod) string {
	return fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
}

func podKeySet(pods []*core.Pod) sets.Set[string] {
	s := sets.New[string]()
	for _, pod := range pods {
		s.Insert(podKey(pod))
	}
	return s
}
