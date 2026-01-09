// Package informer provides a shared informer manager for Kubernetes resources.
package informer

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
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

// WithNodeIndexers sets custom indexers for the node informer.
func WithNodeIndexers(indexers cache.Indexers) Option {
	return func(m *Manager) {
		m.nodes.indexers = indexers
	}
}

// WithPodIndexers sets custom indexers for the pod informer.
func WithPodIndexers(indexers cache.Indexers) Option {
	return func(m *Manager) {
		m.pods.indexers = indexers
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
	if m.started {
		m.log.Warn("informer manager already started")
		return nil
	}

	ctx, cancel := context.WithCancel(ctx)
	m.cancelFunc = cancel

	if err := m.addIndexers(); err != nil {
		cancel()
		return fmt.Errorf("adding indexers: %w", err)
	}

	m.log.Info("starting shared informer factory...")
	m.factory.Start(ctx.Done())

	syncCtx, syncCancel := context.WithTimeout(ctx, m.cacheSyncTimeout)
	defer syncCancel()

	m.log.Info("waiting for informer caches to sync...")
	if !cache.WaitForCacheSync(syncCtx.Done(), m.nodes.HasSynced, m.pods.HasSynced) {
		cancel()
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

func (m *Manager) addIndexers() error {
	if m.nodes.indexers != nil {
		if err := m.nodes.informer.AddIndexers(m.nodes.indexers); err != nil {
			return fmt.Errorf("adding node indexers: %w", err)
		}
	}
	if m.pods.indexers != nil {
		if err := m.pods.informer.AddIndexers(m.pods.indexers); err != nil {
			return fmt.Errorf("adding pod indexers: %w", err)
		}
	}
	return nil
}

func (m *Manager) reportCacheSize(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			nodes := m.nodes.Informer().GetStore().ListKeys()
			size := len(nodes)
			m.log.WithField("cache_size", size).Debug("node informer cache size")
			metrics.SetInformerCacheSize("node", size)

			pods := m.pods.Informer().GetStore().ListKeys()
			size = len(pods)
			m.log.WithField("cache_size", size).Debug("pod informer cache size")
			metrics.SetInformerCacheSize("pod", size)
		}
	}
}
