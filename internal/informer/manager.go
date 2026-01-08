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
	cacheSyncTimeout = 30 * time.Second
)

// Manager manages the global SharedInformerFactory and provides
// access to specific informers and listers.
type Manager struct {
	log       logrus.FieldLogger
	clientset kubernetes.Interface
	factory   informers.SharedInformerFactory

	nodeInformer cache.SharedIndexInformer
	nodeLister   listerv1.NodeLister

	podInformer cache.SharedIndexInformer
	podLister   listerv1.PodLister

	started    bool
	cancelFunc context.CancelFunc
	mu         sync.RWMutex
}

// NewManager creates a new Manager with the given clientset and resync period.
func NewManager(
	log logrus.FieldLogger,
	clientset kubernetes.Interface,
	resyncPeriod time.Duration,
) *Manager {
	factory := informers.NewSharedInformerFactory(clientset, resyncPeriod)

	// Create node informer
	nodeInformer := factory.Core().V1().Nodes().Informer()
	nodeLister := factory.Core().V1().Nodes().Lister()

	// Create pod informer
	podInformer := factory.Core().V1().Pods().Informer()
	podLister := factory.Core().V1().Pods().Lister()

	return &Manager{
		log:          log,
		clientset:    clientset,
		factory:      factory,
		nodeInformer: nodeInformer,
		nodeLister:   nodeLister,
		podInformer:  podInformer,
		podLister:    podLister,
	}
}

// Start starts the informer factory and waits for all caches to sync.
// This method blocks until caches are synchronized or the context is cancelled.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		m.log.Warn("informer manager already started")
		return nil
	}
	m.mu.Unlock()

	// Create a cancellable context for the informer
	ctx, cancel := context.WithCancel(ctx)
	m.mu.Lock()
	m.cancelFunc = cancel
	m.mu.Unlock()

	// Start the factory in a goroutine
	stopCh := make(chan struct{})
	go func() {
		<-ctx.Done()
		close(stopCh)
	}()

	m.log.Info("starting shared informer factory...")
	m.factory.Start(stopCh)

	// Wait for cache sync with timeout
	syncCtx, syncCancel := context.WithTimeout(ctx, cacheSyncTimeout)
	defer syncCancel()

	m.log.Info("waiting for informer caches to sync...")
	if !cache.WaitForCacheSync(syncCtx.Done(), m.nodeInformer.HasSynced, m.podInformer.HasSynced) {
		metrics.IncrementInformerCacheSyncs("node", "failure")
		metrics.IncrementInformerCacheSyncs("pod", "failure")
		return fmt.Errorf("failed to sync informer caches within %v", cacheSyncTimeout)
	}

	metrics.IncrementInformerCacheSyncs("node", "success")
	metrics.IncrementInformerCacheSyncs("pod", "success")

	m.mu.Lock()
	m.started = true
	m.mu.Unlock()

	m.log.Info("informer caches synced successfully")

	// Start background cache size reporter
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
	return m.nodeLister
}

// GetNodeInformer returns the node informer for watching node events.
func (m *Manager) GetNodeInformer() cache.SharedIndexInformer {
	return m.nodeInformer
}

// GetPodLister returns the pod lister for querying the pod cache.
func (m *Manager) GetPodLister() listerv1.PodLister {
	return m.podLister
}

// GetPodInformer returns the pod informer for watching pod events.
func (m *Manager) GetPodInformer() cache.SharedIndexInformer {
	return m.podInformer
}

// GetFactory returns the underlying SharedInformerFactory for advanced use cases.
func (m *Manager) GetFactory() informers.SharedInformerFactory {
	return m.factory
}

// reportCacheSize periodically reports the node and pod cache sizes as metrics.
func (m *Manager) reportCacheSize(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			nodes, err := m.nodeLister.List(labels.Everything())
			if err != nil {
				m.log.WithError(err).Warn("failed to list nodes for cache size metric")
			} else {
				size := len(nodes)
				m.log.WithField("cache_size", size).Debug("node informer cache size")
				metrics.SetInformerCacheSize("node", size)
			}

			pods, err := m.podLister.List(labels.Everything())
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
