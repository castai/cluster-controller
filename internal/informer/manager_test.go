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
	"k8s.io/apimachinery/pkg/runtime"
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
}

func TestManager_Start(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		objects        []runtime.Object
		setupCtx       func(t *testing.T) (context.Context, context.CancelFunc)
		wantErr        bool
		errContains    string
		expectedNodes  int
		expectedPods   int
	}{
		{
			name: "success with resources",
			objects: []runtime.Object{
				&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}},
				&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod-1", Namespace: "default"}},
			},
			setupCtx: func(t *testing.T) (context.Context, context.CancelFunc) {
				return context.WithTimeout(t.Context(), 5*time.Second)
			},
			wantErr:       false,
			expectedNodes: 1,
			expectedPods:  1,
		},
		{
			name:    "success with empty cluster",
			objects: nil,
			setupCtx: func(t *testing.T) (context.Context, context.CancelFunc) {
				return context.WithTimeout(t.Context(), 5*time.Second)
			},
			wantErr:       false,
			expectedNodes: 0,
			expectedPods:  0,
		},
		{
			name:    "context canceled",
			objects: nil,
			setupCtx: func(t *testing.T) (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				return ctx, func() {}
			},
			wantErr:     true,
			errContains: "failed to sync informer caches",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			log := logrus.New()
			log.SetLevel(logrus.ErrorLevel)

			clientset := fake.NewClientset(tt.objects...)
			manager := NewManager(log, clientset, 0)

			ctx, cancel := tt.setupCtx(t)
			defer cancel()

			err := manager.Start(ctx)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					require.Contains(t, err.Error(), tt.errContains)
				}
				return
			}

			require.NoError(t, err)
			defer manager.Stop()

			nodes, err := manager.GetNodeLister().List(labels.Everything())
			require.NoError(t, err)
			require.Len(t, nodes, tt.expectedNodes)

			pods, err := manager.GetPodLister().List(labels.Everything())
			require.NoError(t, err)
			require.Len(t, pods, tt.expectedPods)
		})
	}
}

func TestManager_Start_AlreadyStarted(t *testing.T) {
	t.Parallel()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	clientset := fake.NewClientset()
	manager := NewManager(log, clientset, 0)

	ctx := t.Context()

	err := manager.Start(ctx)
	require.NoError(t, err)
	defer manager.Stop()

	err = manager.Start(ctx)
	require.NoError(t, err)
}

func TestManager_Stop(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(t *testing.T, m *Manager)
	}{
		{
			name:  "stop before start",
			setup: func(t *testing.T, m *Manager) {},
		},
		{
			name: "stop after start",
			setup: func(t *testing.T, m *Manager) {
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				err := m.Start(ctx)
				require.NoError(t, err)
			},
		},
		{
			name: "stop multiple times",
			setup: func(t *testing.T, m *Manager) {
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				err := m.Start(ctx)
				require.NoError(t, err)
				m.Stop()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			log := logrus.New()
			log.SetLevel(logrus.ErrorLevel)

			clientset := fake.NewClientset()
			manager := NewManager(log, clientset, 0)

			tt.setup(t, manager)
			manager.Stop()
		})
	}
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

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
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
	_, err = clientset.CoreV1().Nodes().Create(t.Context(), node2, metav1.CreateOptions{})
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	nodes, err = manager.GetNodeLister().List(labels.Everything())
	require.NoError(t, err)
	require.Len(t, nodes, 2)
}
