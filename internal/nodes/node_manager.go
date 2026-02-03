package nodes

import (
	"context"
	"fmt"
	"time"

	"github.com/castai/cluster-controller/internal/informer"
	"github.com/castai/cluster-controller/internal/k8s"
	"github.com/castai/cluster-controller/internal/logger"
	"github.com/castai/cluster-controller/internal/waitext"
	"github.com/samber/lo"
	"github.com/sirupsen/logrus"
	core "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/kubectl/pkg/drain"
)

type Manager interface {
	Evict(ctx context.Context, fromNode, castNamespace string, skipDeletedTimeoutSeconds int) ([]*core.Pod, error)
	Drain(ctx context.Context, fromNode, castNamespace string, skipDeletedTimeoutSeconds int) ([]*core.Pod, error)
}

type ManagerConfig struct {
	podEvictRetryDelay            time.Duration
	podsTerminationWaitRetryDelay time.Duration
}

type manager struct {
	indexer cache.Indexer
	client  *k8s.Client
	cfg     ManagerConfig
	log     logrus.FieldLogger
}

func NewManager(
	indexer cache.Indexer,
	client *k8s.Client,
	log logrus.FieldLogger,
	cfg ManagerConfig,
) Manager {
	m := &manager{
		indexer: indexer,
		client:  client,
		cfg:     cfg,
		log:     log,
	}
	return m
}

func (m *manager) Drain(ctx context.Context, fromNode, castNamespace string, skipDeletedTimeoutSeconds int) ([]*core.Pod, error) {
	logger := logger.FromContext(ctx, m.log)

	logger.Info("starting drain")
	pods, err := m.list(ctx, fromNode)
	if err != nil {
		return nil, err
	}

	partitioned := k8s.PartitionPodsForEviction(pods, castNamespace, skipDeletedTimeoutSeconds)

	if len(partitioned.CastPods) == 0 && len(partitioned.Evictable) == 0 {
		return []*core.Pod{}, nil
	}

	toEvict := make([]core.Pod, 0, len(partitioned.CastPods)+len(partitioned.Evictable))
	toEvict = append(toEvict, partitioned.Evictable...)
	toEvict = append(toEvict, partitioned.CastPods...)

	_, ignored, err := m.tryDrain(ctx, toEvict, logger)

	err = m.waitTerminaition(ctx, fromNode, ignored)
	if err != nil {
		return []*core.Pod{}, err
	}

	logger.Info("drain finished")

	return ignored, nil
}

func (m *manager) tryDrain(ctx context.Context, toEvict []core.Pod, log logrus.FieldLogger) ([]*core.Pod, []*core.Pod, error) {
	groupVersion, err := drain.CheckEvictionSupport(m.client.Clientset())
	if err != nil {
		return nil, nil, err
	}
	evictPod := func(ctx context.Context, pod core.Pod) error {
		return m.client.EvictPod(ctx, pod, m.cfg.podEvictRetryDelay, groupVersion)
	}

	successful, podsWithFailedEviction := m.client.ExecuteBatchPodActions(ctx, toEvict, evictPod, "evict-pod")
	var failed []*v1.Pod
	var failedPodsError *k8s.PodFailedActionError
	if len(podsWithFailedEviction) > 0 {
		podErrors := lo.Map(podsWithFailedEviction, func(failure k8s.PodActionFailure, _ int) error {
			return fmt.Errorf("pod %s/%s failed eviction: %w", failure.Pod.Namespace, failure.Pod.Name, failure.Err)
		})
		failedPodsError = &k8s.PodFailedActionError{
			Action: "evict",
			Errors: podErrors,
		}
		log.Warnf("some pods failed eviction, will ignore for termination wait: %v", failedPodsError)
		failed = lo.Map(podsWithFailedEviction, func(failure k8s.PodActionFailure, _ int) *v1.Pod {
			return failure.Pod
		})
	}

	return successful, failed, nil
}

func (m *manager) Evict(ctx context.Context, fromNode, castNamespace string, skipDeletedTimeoutSeconds int) ([]*core.Pod, error) {
	logger := logger.FromContext(ctx, m.log)

	logger.Info("starting eviction")

	pods, err := m.list(ctx, fromNode)
	if err != nil {
		return nil, err
	}

	partitioned := k8s.PartitionPodsForEviction(pods, castNamespace, skipDeletedTimeoutSeconds)

	if len(partitioned.CastPods) == 0 && len(partitioned.Evictable) == 0 {
		return []*core.Pod{}, nil
	}

	toEvict := make([]core.Pod, 0, len(partitioned.CastPods)+len(partitioned.Evictable))
	toEvict = append(toEvict, partitioned.Evictable...)
	toEvict = append(toEvict, partitioned.CastPods...)

	_, ignored, err := m.tryEvict(ctx, toEvict, logger)

	err = m.waitTerminaition(ctx, fromNode, ignored)
	if err != nil {
		return []*core.Pod{}, err
	}

	logger.Info("eviction finished")

	return ignored, nil
}

func (m *manager) list(ctx context.Context, fromNode string) ([]core.Pod, error) {
	objects, err := m.indexer.ByIndex(informer.PodIndexerName, fromNode)
	if err != nil {
		return nil, err
	}

	pods := make([]core.Pod, 0, len(objects))
	for _, obj := range objects {
		pod, ok := obj.(*core.Pod)
		if !ok {
			continue
		}
		pods = append(pods, *pod)
	}

	return pods, nil
}

func (m *manager) tryEvict(ctx context.Context, toEvict []core.Pod, log logrus.FieldLogger) ([]*core.Pod, []*core.Pod, error) {
	groupVersion, err := drain.CheckEvictionSupport(m.client.Clientset())
	if err != nil {
		return nil, nil, err
	}
	evictPod := func(ctx context.Context, pod core.Pod) error {
		return m.client.EvictPod(ctx, pod, m.cfg.podEvictRetryDelay, groupVersion)
	}

	successful, podsWithFailedEviction := m.client.ExecuteBatchPodActions(ctx, toEvict, evictPod, "evict-pod")
	var failed []*v1.Pod
	var failedPodsError *k8s.PodFailedActionError
	if len(podsWithFailedEviction) > 0 {
		podErrors := lo.Map(podsWithFailedEviction, func(failure k8s.PodActionFailure, _ int) error {
			return fmt.Errorf("pod %s/%s failed eviction: %w", failure.Pod.Namespace, failure.Pod.Name, failure.Err)
		})
		failedPodsError = &k8s.PodFailedActionError{
			Action: "evict",
			Errors: podErrors,
		}
		log.Warnf("some pods failed eviction, will ignore for termination wait: %v", failedPodsError)
		failed = lo.Map(podsWithFailedEviction, func(failure k8s.PodActionFailure, _ int) *v1.Pod {
			return failure.Pod
		})
	}

	return successful, failed, nil
}

func (m *manager) waitTerminaition(ctx context.Context, fromNode string, ignored []*core.Pod) error {
	logger := logger.FromContext(ctx, m.log)
	// Check if context is canceled before starting any work.
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		// Continue with the work.
	}

	ignoredMap := make(map[string]struct{})
	for _, pod := range ignored {
		ignoredMap[fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)] = struct{}{}
	}

	logger.Infof("starting wait for pod termination from informer, %d pods in ignore list", len(ignored))
	return waitext.Retry(
		ctx,
		waitext.NewConstantBackoff(m.cfg.podsTerminationWaitRetryDelay),
		waitext.Forever,
		func(ctx context.Context) (bool, error) {
			pods, err := m.list(ctx, fromNode)
			if err != nil {
				return true, fmt.Errorf("listing %q pods to be terminated: %w", fromNode, err)
			}

			remaining := lo.Map(pods, func(p v1.Pod, _ int) string {
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
