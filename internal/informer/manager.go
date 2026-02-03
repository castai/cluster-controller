// Package informer provides a shared informer manager for Kubernetes resources.
package informer

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	authorizationv1 "k8s.io/api/authorization/v1"
	core "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	listerv1 "k8s.io/client-go/listers/core/v1"
	listerstoragev1 "k8s.io/client-go/listers/storage/v1"
	"k8s.io/client-go/tools/cache"

	"github.com/castai/cluster-controller/internal/metrics"
)

const (
	defaultCacheSyncTimeout = 30 * time.Second
)

// VAPermissionError indicates that required RBAC permissions for VolumeAttachments are missing.
type VAPermissionError struct {
	// MissingVerb is the verb that was denied (e.g., "get", "list", "watch").
	MissingVerb string
}

func (e *VAPermissionError) Error() string {
	return fmt.Sprintf("missing '%s' permission for volumeattachments.storage.k8s.io", e.MissingVerb)
}

// Manager manages the global SharedInformerFactory and provides
// access to specific informers and listers.
type Manager struct {
	log              logrus.FieldLogger
	clientset        kubernetes.Interface
	factory          informers.SharedInformerFactory
	cacheSyncTimeout time.Duration

	nodes             NodeInformer
	pods              *podInformer
	volumeAttachments *vaInformer

	started     bool
	vaAvailable bool
	cancelFunc  context.CancelFunc
	mu          sync.RWMutex
}

// Option is a functional option for configuring the Manager.
type Option func(*Manager)

// WithCacheSyncTimeout sets the timeout for waiting for informer caches to sync.
func WithCacheSyncTimeout(timeout time.Duration) Option {
	return func(m *Manager) {
		m.cacheSyncTimeout = timeout
	}
}

func EnablePodInformer() Option {
	return func(m *Manager) {
		m.pods = &podInformer{
			informer: m.factory.Core().V1().Pods().Informer(),
			lister:   m.factory.Core().V1().Pods().Lister(),
		}
	}
}

func EnableNodeInformer() Option {
	return func(m *Manager) {
		m.nodes = NewNodeInformer(
			m.factory.Core().V1().Nodes().Informer(),
			m.factory.Core().V1().Nodes().Lister(),
		)
	}
}

// WithNodeIndexers sets custom indexers for the node informer.
func WithNodeIndexers(indexers cache.Indexers) Option {
	return func(n *Manager) {
		n.nodes.SetIndexers(indexers)
	}
}

func WithDefaultPodNodeNameIndexer() Option {
	return WithPodIndexers(cache.Indexers{
		PodIndexerName: func(obj any) ([]string, error) {
			pod, ok := obj.(core.Pod)
			if !ok {
				return nil, nil
			}
			return []string{pod.Spec.NodeName}, nil
		},
	})
}

// WithPodIndexers sets custom indexers for the pod informer.
func WithPodIndexers(indexers cache.Indexers) Option {
	return func(n *Manager) {
		n.pods.indexers = indexers
	}
}

// WithVAIndexers sets custom indexers for the VolumeAttachment informer.
func WithVAIndexers(indexers cache.Indexers) Option {
	return func(m *Manager) {
		m.volumeAttachments.indexers = indexers
	}
}

// WithDefaultVANodeNameIndexer adds the default spec.nodeName indexer for VolumeAttachments.
// This is commonly used to look up VolumeAttachments by node name.
func WithDefaultVANodeNameIndexer() Option {
	return WithVAIndexers(cache.Indexers{
		VANodeNameIndexer: func(obj any) ([]string, error) {
			va, ok := obj.(*storagev1.VolumeAttachment)
			if !ok {
				return nil, nil
			}
			return []string{va.Spec.NodeName}, nil
		},
	})
}

// NewManager creates a new Manager with the given clientset and resync period.
func NewManager(
	log logrus.FieldLogger,
	clientset kubernetes.Interface,
	resyncPeriod time.Duration,
	opts ...Option,
) *Manager {
	factory := informers.NewSharedInformerFactory(clientset, resyncPeriod)

	volumeAttachments := &vaInformer{
		informer: factory.Storage().V1().VolumeAttachments().Informer(),
		lister:   factory.Storage().V1().VolumeAttachments().Lister(),
	}

	m := &Manager{
		log:               log,
		clientset:         clientset,
		factory:           factory,
		cacheSyncTimeout:  defaultCacheSyncTimeout,
		nodes:             nil,
		pods:              nil,
		volumeAttachments: volumeAttachments,
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

	// Sync optional node/pod informers - fail if enabled but don't sync
	if m.nodes != nil {
		if err := m.nodes.Start(ctx); err != nil {
			cancel()
			return fmt.Errorf("starting node informer: %w", err)
		}

		m.log.Info("waiting for node informer cache to sync...")
		if !cache.WaitForCacheSync(syncCtx.Done(), m.nodes.HasSynced) {
			cancel()
			return fmt.Errorf("failed to sync node informer cache within %v", m.cacheSyncTimeout)
		}
		metrics.IncrementInformerCacheSyncs("node", "success")
		m.log.Info("node informer cache synced successfully")
	}

	if m.pods != nil {
		m.log.Info("waiting for pod informer cache to sync...")
		if !cache.WaitForCacheSync(syncCtx.Done(), m.pods.HasSynced) {
			cancel()
			return fmt.Errorf("failed to sync pod informer cache within %v", m.cacheSyncTimeout)
		}
		metrics.IncrementInformerCacheSyncs("pod", "success")
		m.log.Info("pod informer cache synced successfully")
	}

	// Check VA permissions and sync informer
	m.vaAvailable = m.syncVAInformer(ctx, syncCtx)

	m.started = true

	go m.reportCacheSize(ctx)

	return nil
}

// syncVAInformer checks RBAC permissions and syncs the VolumeAttachment informer.
// Returns true if the VA informer is available and synced successfully, false otherwise.
func (m *Manager) syncVAInformer(ctx, syncCtx context.Context) bool {
	m.log.Info("checking VolumeAttachment RBAC permissions...")
	if err := m.checkVAPermissions(ctx); err != nil {
		var permErr *VAPermissionError
		if errors.As(err, &permErr) {
			m.log.Warnf("%v. VA wait feature will be disabled. "+
				"Grant get/list/watch permissions on volumeattachments.storage.k8s.io "+
				"and restart controller.", permErr)
			metrics.IncrementInformerCacheSyncs("volumeattachment", "rbac_denied")
		} else {
			m.log.Warnf("failed to verify VolumeAttachment permissions: %v. "+
				"VA wait feature will be disabled.", err)
			metrics.IncrementInformerCacheSyncs("volumeattachment", "rbac_check_failed")
		}
		return false
	}

	m.log.Info("waiting for VolumeAttachment informer cache to sync...")
	if !cache.WaitForCacheSync(syncCtx.Done(), m.volumeAttachments.HasSynced) {
		m.log.Warn("VolumeAttachment informer failed to sync. VA wait feature will be disabled.")
		metrics.IncrementInformerCacheSyncs("volumeattachment", "sync_failed")
		return false
	}

	metrics.IncrementInformerCacheSyncs("volumeattachment", "success")
	m.log.Info("VolumeAttachment informer cache synced successfully")
	return true
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
	if m.nodes == nil {
		return nil
	}
	return m.nodes.Lister()
}

// GetNodeInformer returns the node informer for watching node events.
func (m *Manager) GetNodeInformer() NodeInformer {
	if m.nodes == nil {
		return nil
	}
	return m.nodes
}

// GetPodLister returns the pod lister for querying the pod cache.
func (m *Manager) GetPodLister() listerv1.PodLister {
	if m.pods == nil {
		return nil
	}
	return m.pods.Lister()
}

// GetPodInformer returns the pod informer for watching pod events.
func (m *Manager) GetPodInformer() cache.SharedIndexInformer {
	if m.pods == nil {
		return nil
	}
	return m.pods.Informer()
}

// IsVAAvailable indicates whether the VolumeAttachment informer is available.
func (m *Manager) IsVAAvailable() bool {
	return m.vaAvailable
}

// GetVALister returns the VolumeAttachment lister for querying the VA cache.
// Returns nil if the VA informer is not available or not synced.
func (m *Manager) GetVALister() listerstoragev1.VolumeAttachmentLister {
	if !m.vaAvailable {
		return nil
	}
	return m.volumeAttachments.Lister()
}

// GetVAInformer returns the VolumeAttachment informer for watching VA events.
// Returns nil if the VA informer is not available or not synced.
func (m *Manager) GetVAInformer() cache.SharedIndexInformer {
	if !m.vaAvailable {
		return nil
	}
	return m.volumeAttachments.Informer()
}

// GetVAIndexer returns the VolumeAttachment indexer for indexed lookups.
// Use with VANodeNameIndexer to look up VolumeAttachments by node name.
// Returns nil if the VA informer is not available or not synced.
func (m *Manager) GetVAIndexer() cache.Indexer {
	if !m.vaAvailable {
		return nil
	}
	return m.volumeAttachments.Informer().GetIndexer()
}

// GetFactory returns the underlying SharedInformerFactory for advanced use cases.
func (m *Manager) GetFactory() informers.SharedInformerFactory {
	return m.factory
}

// checkVAPermissions verifies the service account has required permissions for VolumeAttachments.
// Returns nil if all permissions are available.
// Returns *VAPermissionError if any permission is missing (fails fast on first missing).
// Returns wrapped error if the permission check itself fails.
func (m *Manager) checkVAPermissions(ctx context.Context) error {
	requiredVerbs := []string{"get", "list", "watch"}

	for _, verb := range requiredVerbs {
		sar := &authorizationv1.SelfSubjectAccessReview{
			Spec: authorizationv1.SelfSubjectAccessReviewSpec{
				ResourceAttributes: &authorizationv1.ResourceAttributes{
					Group:    "storage.k8s.io",
					Resource: "volumeattachments",
					Verb:     verb,
				},
			},
		}

		result, err := m.clientset.AuthorizationV1().SelfSubjectAccessReviews().Create(
			ctx, sar, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("checking '%s' permission for volumeattachments: %w", verb, err)
		}

		if !result.Status.Allowed {
			return &VAPermissionError{MissingVerb: verb}
		}
	}

	m.log.Debug("all VolumeAttachment permissions verified: get, list, watch")
	return nil
}

func (m *Manager) addIndexers() error {
	if m.nodes != nil && m.nodes.Indexers() != nil {
		if err := m.nodes.Informer().AddIndexers(m.nodes.Indexers()); err != nil {
			return fmt.Errorf("adding node indexers: %w", err)
		}
	}
	if m.pods != nil && m.pods.indexers != nil {
		if err := m.pods.informer.AddIndexers(m.pods.indexers); err != nil {
			return fmt.Errorf("adding pod indexers: %w", err)
		}
	}
	if m.volumeAttachments.indexers != nil {
		if err := m.volumeAttachments.informer.AddIndexers(m.volumeAttachments.indexers); err != nil {
			return fmt.Errorf("adding volumeattachment indexers: %w", err)
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
			if m.nodes != nil {
				nodes := m.nodes.Informer().GetStore().ListKeys()
				size := len(nodes)
				m.log.WithField("cache_size", size).Debug("node informer cache size")
				metrics.SetInformerCacheSize("node", size)
			}

			if m.pods != nil {
				pods := m.pods.Informer().GetStore().ListKeys()
				size := len(pods)
				m.log.WithField("cache_size", size).Debug("pod informer cache size")
				metrics.SetInformerCacheSize("pod", size)
			}

			if m.vaAvailable {
				was := m.volumeAttachments.Informer().GetStore().ListKeys()
				size := len(was)
				m.log.WithField("cache_size", size).Debug("volumeattachment informer cache size")
				metrics.SetInformerCacheSize("volumeattachment", size)
			}
		}
	}
}
