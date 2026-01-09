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
	clientset := fake.NewClientset()

	manager := NewManager(log, clientset, time.Hour)

	require.NotNil(t, manager)
	require.NotNil(t, manager.GetFactory())
	require.NotNil(t, manager.GetNodeLister())
	require.NotNil(t, manager.GetNodeInformer())
	require.NotNil(t, manager.GetPodLister())
	require.NotNil(t, manager.GetPodInformer())
	require.False(t, manager.IsStarted())
}

func TestManager_Start_Success(t *testing.T) {
	t.Parallel()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pod-1", Namespace: "default"},
	}

	clientset := fake.NewClientset(node, pod)
	manager := NewManager(log, clientset, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := manager.Start(ctx)
	require.NoError(t, err)
	require.True(t, manager.IsStarted())
	defer manager.Stop()

	nodes, err := manager.GetNodeLister().List(labels.Everything())
	require.NoError(t, err)
	require.Len(t, nodes, 1)

	pods, err := manager.GetPodLister().List(labels.Everything())
	require.NoError(t, err)
	require.Len(t, pods, 1)
}

func TestManager_Start_AlreadyStarted(t *testing.T) {
	t.Parallel()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	clientset := fake.NewClientset()
	manager := NewManager(log, clientset, 0)

	ctx := context.Background()

	err := manager.Start(ctx)
	require.NoError(t, err)
	defer manager.Stop()

	err = manager.Start(ctx)
	require.NoError(t, err)
	require.True(t, manager.IsStarted())
}

func TestManager_Start_ContextCanceled(t *testing.T) {
	t.Parallel()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	clientset := fake.NewClientset()
	manager := NewManager(log, clientset, 0)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := manager.Start(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to sync informer caches")
	require.False(t, manager.IsStarted())
}

func TestManager_Stop(t *testing.T) {
	t.Parallel()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	clientset := fake.NewClientset()
	manager := NewManager(log, clientset, 0)

	manager.Stop()
	require.False(t, manager.IsStarted())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := manager.Start(ctx)
	require.NoError(t, err)
	require.True(t, manager.IsStarted())

	manager.Stop()
	require.False(t, manager.IsStarted())

	manager.Stop()
	require.False(t, manager.IsStarted())
}

func TestManager_CacheUpdates(t *testing.T) {
	t.Parallel()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
	}

	clientset := fake.NewClientset(node)
	manager := NewManager(log, clientset, 0)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := manager.Start(ctx)
	require.NoError(t, err)
	defer manager.Stop()

	nodes, err := manager.GetNodeLister().List(labels.Everything())
	require.NoError(t, err)
	require.Len(t, nodes, 1)

	node2 := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-2"},
	}
	_, err = clientset.CoreV1().Nodes().Create(context.Background(), node2, metav1.CreateOptions{})
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	nodes, err = manager.GetNodeLister().List(labels.Everything())
	require.NoError(t, err)
	require.Len(t, nodes, 2)
}
