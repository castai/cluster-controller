package informer

import (
	"context"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes/fake"
)

func TestNewManager(t *testing.T) {
	t.Parallel()

	log := logrus.New()
	clientset := fake.NewSimpleClientset()
	resyncPeriod := 12 * time.Hour

	manager := NewManager(log, clientset, resyncPeriod)

	require.NotNil(t, manager)
	require.NotNil(t, manager.factory)
	require.NotNil(t, manager.nodeInformer)
	require.NotNil(t, manager.nodeLister)
	require.False(t, manager.IsStarted())
}

func TestManager_Start_Success(t *testing.T) {
	t.Parallel()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel) // Reduce test noise

	// Create fake clientset with some nodes
	node1 := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node-1",
			Labels: map[string]string{
				"test": "true",
			},
		},
	}
	node2 := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node-2",
		},
	}

	clientset := fake.NewSimpleClientset(node1, node2)
	manager := NewManager(log, clientset, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Start should succeed and sync caches
	err := manager.Start(ctx)
	require.NoError(t, err)
	require.True(t, manager.IsStarted())

	// Should be able to list nodes from cache
	lister := manager.GetNodeLister()
	require.NotNil(t, lister)

	nodes, err := lister.List(labels.Everything())
	require.NoError(t, err)
	require.Len(t, nodes, 2)

	// Cleanup
	manager.Stop()
	require.False(t, manager.IsStarted())
}

func TestManager_Start_AlreadyStarted(t *testing.T) {
	t.Parallel()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	clientset := fake.NewSimpleClientset()
	manager := NewManager(log, clientset, 0)

	ctx := context.Background()

	// Start first time
	err := manager.Start(ctx)
	require.NoError(t, err)
	require.True(t, manager.IsStarted())

	// Start second time should return nil without error
	err = manager.Start(ctx)
	require.NoError(t, err)
	require.True(t, manager.IsStarted())

	manager.Stop()
}

func TestManager_Start_ContextCanceled(t *testing.T) {
	t.Parallel()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	clientset := fake.NewSimpleClientset()
	manager := NewManager(log, clientset, 0)

	// Create a context that's already canceled
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Start should fail due to context cancellation
	err := manager.Start(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to sync informer caches")
	require.False(t, manager.IsStarted())
}

func TestManager_GetNodeLister(t *testing.T) {
	t.Parallel()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
			Labels: map[string]string{
				"env": "test",
			},
		},
		Spec: corev1.NodeSpec{
			ProviderID: "test-provider-id",
		},
	}

	clientset := fake.NewSimpleClientset(node)
	manager := NewManager(log, clientset, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := manager.Start(ctx)
	require.NoError(t, err)
	defer manager.Stop()

	lister := manager.GetNodeLister()
	require.NotNil(t, lister)

	// Test Get by name
	retrievedNode, err := lister.Get("test-node")
	require.NoError(t, err)
	require.Equal(t, "test-node", retrievedNode.Name)
	require.Equal(t, "test-provider-id", retrievedNode.Spec.ProviderID)

	// Test Get non-existent node
	_, err = lister.Get("non-existent")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")

	// Test List all nodes
	nodes, err := lister.List(labels.Everything())
	require.NoError(t, err)
	require.Len(t, nodes, 1)
}

func TestManager_Stop(t *testing.T) {
	t.Parallel()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	clientset := fake.NewSimpleClientset()
	manager := NewManager(log, clientset, 0)

	// Stop when not started should be safe
	manager.Stop()
	require.False(t, manager.IsStarted())

	// Start and then stop
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := manager.Start(ctx)
	require.NoError(t, err)
	require.True(t, manager.IsStarted())

	manager.Stop()
	require.False(t, manager.IsStarted())

	// Multiple stops should be safe
	manager.Stop()
	require.False(t, manager.IsStarted())
}

func TestManager_IsStarted(t *testing.T) {
	t.Parallel()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	clientset := fake.NewSimpleClientset()
	manager := NewManager(log, clientset, 0)

	// Initially not started
	require.False(t, manager.IsStarted())

	// After start
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := manager.Start(ctx)
	require.NoError(t, err)
	require.True(t, manager.IsStarted())

	// After stop
	manager.Stop()
	require.False(t, manager.IsStarted())
}

func TestManager_GetFactory(t *testing.T) {
	t.Parallel()

	log := logrus.New()
	clientset := fake.NewSimpleClientset()
	manager := NewManager(log, clientset, 0)

	factory := manager.GetFactory()
	require.NotNil(t, factory)
}

func TestManager_CacheUpdates(t *testing.T) {
	t.Parallel()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	// Start with one node
	node1 := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node-1",
		},
	}

	clientset := fake.NewSimpleClientset(node1)
	manager := NewManager(log, clientset, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := manager.Start(ctx)
	require.NoError(t, err)
	defer manager.Stop()

	lister := manager.GetNodeLister()

	// Should see initial node
	nodes, err := lister.List(labels.Everything())
	require.NoError(t, err)
	require.Len(t, nodes, 1)

	// Add another node to the fake clientset
	node2 := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node-2",
		},
	}
	_, err = clientset.CoreV1().Nodes().Create(context.Background(), node2, metav1.CreateOptions{})
	require.NoError(t, err)

	// Wait a bit for the informer to pick up the change
	time.Sleep(100 * time.Millisecond)

	// Should now see both nodes
	nodes, err = lister.List(labels.Everything())
	require.NoError(t, err)
	require.Len(t, nodes, 2)
}
