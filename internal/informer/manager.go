// Package informer provides a shared informer manager for Kubernetes resources.
package informer

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	listerv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"

	"github.com/castai/cluster-controller/internal/metrics"
)

const (
	defaultCacheSyncTimeout = 30 * time.Second
)

// Manager manages the global SharedInformerFactory and provides
// access to specific informers and listers.
type Manager struct {
	log              logrus.FieldLogger
	factory          informers.SharedInformerFactory
	cacheSyncTimeout time.Duration

	nodes *nodeInformer
	pods  *podInformer

	started    bool
	cancelFunc context.CancelFunc
	mu         sync.RWMutex
}

// Option is a functional option for configuring the Manager.
type Option func(*Manager)

// WithCacheSyncTimeout sets the timeout for waiting for informer caches to sync.
func WithCacheSyncTimeout(timeout time.Duration) Option {
	return func(m *Manager) {
		m.cacheSyncTimeout = timeout
	}
}

// NewManager creates a new Manager with the given clientset and resync period.
func NewManager(
	log logrus.FieldLogger,
	clientset kubernetes.Interface,
	resyncPeriod time.Duration,
	opts ...Option,
) *Manager {
	factory := informers.NewSharedInformerFactory(clientset, resyncPeriod)

	nodes := &nodeInformer{
		informer: factory.Core().V1().Nodes().Informer(),
		lister:   factory.Core().V1().Nodes().Lister(),
	}

	pods := &podInformer{
		informer: factory.Core().V1().Pods().Informer(),
		lister:   factory.Core().V1().Pods().Lister(),
	}

	m := &Manager{
		log:              log,
		factory:          factory,
		cacheSyncTimeout: defaultCacheSyncTimeout,
		nodes:            nodes,
		pods:             pods,
	}

	for _, opt := range opts {
		opt(m)
	}

	return m
}

// Start starts the informer factory and waits for all caches to sync.
// This method blocks until caches are synchronized or the context is canceled.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		m.log.Warn("informer manager already started")
		return nil
	}
	defer m.mu.Unlock()

	ctx, cancel := context.WithCancel(ctx)
	m.cancelFunc = cancel

	stopCh := make(chan struct{})
	go func() {
		<-ctx.Done()
		close(stopCh)
	}()

	m.log.Info("starting shared informer factory...")
	m.factory.Start(stopCh)

	syncCtx, syncCancel := context.WithTimeout(ctx, m.cacheSyncTimeout)
	defer syncCancel()

	m.log.Info("waiting for informer caches to sync...")
	if !cache.WaitForCacheSync(syncCtx.Done(), m.nodes.HasSynced, m.pods.HasSynced) {
		cancel()
		metrics.IncrementInformerCacheSyncs("node", "failure")
		metrics.IncrementInformerCacheSyncs("pod", "failure")
		return fmt.Errorf("failed to sync informer caches within %v", m.cacheSyncTimeout)
	}

	metrics.IncrementInformerCacheSyncs("node", "success")
	metrics.IncrementInformerCacheSyncs("pod", "success")

	m.started = true

	m.log.Info("informer caches synced successfully")

	go m.reportCacheSize(ctx)

	return nil
}

// Stop gracefully stops the informer factory.
func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if !m.started {
		return
	}

	m.log.Info("stopping informer manager...")
	if m.cancelFunc != nil {
		m.cancelFunc()
		m.cancelFunc = nil
	}
	m.started = false
	m.log.Info("informer manager stopped")
}

// IsStarted returns true if the informer manager has been started and caches are synced.
func (m *Manager) IsStarted() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.started
}

// GetNodeLister returns the node lister for querying the node cache.
func (m *Manager) GetNodeLister() listerv1.NodeLister {
	return m.nodes.Lister()
}

// GetNodeInformer returns the node informer for watching node events.
func (m *Manager) GetNodeInformer() cache.SharedIndexInformer {
	return m.nodes.Informer()
}

// GetPodLister returns the pod lister for querying the pod cache.
func (m *Manager) GetPodLister() listerv1.PodLister {
	return m.pods.Lister()
}

// GetPodInformer returns the pod informer for watching pod events.
func (m *Manager) GetPodInformer() cache.SharedIndexInformer {
	return m.pods.Informer()
}

// GetFactory returns the underlying SharedInformerFactory for advanced use cases.
func (m *Manager) GetFactory() informers.SharedInformerFactory {
	return m.factory
}

func (m *Manager) reportCacheSize(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			nodes, err := m.nodes.Lister().List(labels.Everything())
			if err != nil {
				m.log.WithError(err).Warn("failed to list nodes for cache size metric")
			} else {
				size := len(nodes)
				m.log.WithField("cache_size", size).Debug("node informer cache size")
				metrics.SetInformerCacheSize("node", size)
			}

			pods, err := m.pods.Lister().List(labels.Everything())
			if err != nil {
				m.log.WithError(err).Warn("failed to list pods for cache size metric")
			} else {
				size := len(pods)
				m.log.WithField("cache_size", size).Debug("pod informer cache size")
				metrics.SetInformerCacheSize("pod", size)
			}
		}
	}
}
