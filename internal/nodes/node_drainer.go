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
	client *k8s.Client
	cfg    DrainerConfig
	log    logrus.FieldLogger
}

func NewDrainer(
	pods informer.PodInformer,
	client *k8s.Client,
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
	logger := logger.FromContext(ctx, d.log)

	logger.Info("starting drain")
	pods, err := d.list(ctx, data.Node)
	if err != nil {
		return nil, err
	}

	toEvict := d.prioritizePods(pods, data.CastNamespace, data.SkipDeletedTimeoutSeconds)
	if len(toEvict) == 0 {
		return []*core.Pod{}, nil
	}

	_, failed, err := d.tryDrain(ctx, toEvict, data.DeleteOptions)
	if err != nil && !errors.Is(err, &k8s.PodFailedActionError{}) {
		return nil, err
	}

	err = d.waitTerminaition(ctx, data.Node, failed)
	if err != nil {
		return []*core.Pod{}, err
	}

	logger.Info("drain finished")

	return failed, nil
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
	logger := logger.FromContext(ctx, d.log)

	logger.Info("starting eviction")

	pods, err := d.list(ctx, data.Node)
	if err != nil {
		return nil, err
	}

	toEvict := d.prioritizePods(pods, data.CastNamespace, data.SkipDeletedTimeoutSeconds)
	if len(toEvict) == 0 {
		return []*core.Pod{}, nil
	}

	_, ignored, err := d.tryEvict(ctx, toEvict)
	if err != nil && !errors.Is(err, &k8s.PodFailedActionError{}) {
		return nil, err
	}

	err = d.waitTerminaition(ctx, data.Node, ignored)
	if err != nil {
		return []*core.Pod{}, err
	}

	logger.Info("eviction finished")

	return ignored, nil
}

func (d *drainer) tryEvict(ctx context.Context, toEvict []*core.Pod) ([]*core.Pod, []*core.Pod, error) {
	groupVersion, err := drain.CheckEvictionSupport(d.client.Clientset())
	if err != nil {
		return nil, nil, err
	}
	evictPod := func(ctx context.Context, pod core.Pod) error {
		return d.client.EvictPod(ctx, pod, d.cfg.PodEvictRetryDelay, groupVersion)
	}

	successful, podsWithFailedAction := d.client.ExecuteBatchPodActions(ctx, toEvict, evictPod, "evict-pod")
	failed, err := d.handleFailures(ctx, "eviction", podsWithFailedAction)
	return successful, failed, err
}

func (d *drainer) list(_ context.Context, fromNode string) ([]*core.Pod, error) {
	podPtrs, err := d.pods.ListByNode(fromNode)
	if err != nil {
		return nil, err
	}

	pods := make([]*core.Pod, 0, len(podPtrs))
	pods = append(pods, podPtrs...)
	return pods, nil
}

func (d *drainer) prioritizePods(pods []*core.Pod, castNamespace string, skipDeletedTimeoutSeconds int) []*core.Pod {
	partitioned := k8s.PartitionPodsForEviction(pods, castNamespace, skipDeletedTimeoutSeconds)

	if len(partitioned.CastPods) == 0 && len(partitioned.Evictable) == 0 {
		return []*core.Pod{}
	}

	toRemove := make([]*core.Pod, 0, len(partitioned.Evictable))
	toRemove = append(toRemove, partitioned.Evictable...)

	return toRemove
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

func (d *drainer) waitTerminaition(ctx context.Context, fromNode string, ignored []*core.Pod) error {
	logger := logger.FromContext(ctx, d.log)
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	ignoredMap := make(map[string]struct{})
	for _, pod := range ignored {
		ignoredMap[fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)] = struct{}{}
	}

	logger.Infof("starting wait for pod termination from informer, %d pods in ignore list", len(ignored))
	return waitext.Retry(
		ctx,
		waitext.NewConstantBackoff(d.cfg.PodsTerminationWaitRetryDelay),
		waitext.Forever,
		func(ctx context.Context) (bool, error) {
			pods, err := d.list(ctx, fromNode)
			if err != nil {
				return true, fmt.Errorf("listing %q pods to be terminated: %w", fromNode, err)
			}

			remaining := lo.Map(pods, func(p *core.Pod, _ int) string {
				return fmt.Sprintf("%s/%s", p.Namespace, p.Name)
			})

			if len(ignored) > 0 {
				remaining = lo.Filter(remaining, func(podName string, _ int) bool {
					_, ok := ignoredMap[podName]
					return !ok
				})
			}
			if remainingPods := len(remaining); remainingPods > 0 {
				return true, fmt.Errorf("waiting for %d pods (%v) to be terminated on node %v", remainingPods, remaining, fromNode)
			}
			return false, nil
		},
		func(err error) {
			logger.Warnf("waiting for pod termination on node %v, will retry: %v", fromNode, err)
		},
	)
}
