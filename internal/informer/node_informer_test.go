package informer

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	k8stest "k8s.io/client-go/testing"
)

func TestNodeInformer_Informer(t *testing.T) {
	t.Parallel()

	log := logrus.New()
	clientset := fake.NewClientset()
	manager := NewManager(log, clientset, time.Hour)

	informer := manager.nodes.Informer()
	lister := manager.nodes.Lister()

	require.False(t, manager.nodes.HasSynced())
	require.NotNil(t, informer)
	require.NotNil(t, lister)
}

func TestNodeInformer_Wait(t *testing.T) {
	t.Parallel()

	nodeReady := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{
					Type:   corev1.NodeReady,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}

	nodeNotReady := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-node",
		},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{
					Type:   corev1.NodeReady,
					Status: corev1.ConditionFalse,
				},
			},
		},
	}

	otherNodeReady := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "other-node",
		},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{
					Type:   corev1.NodeReady,
					Status: corev1.ConditionTrue,
				},
			},
		},
	}

	conditionReady := func(node *corev1.Node) (bool, error) {
		for _, cond := range node.Status.Conditions {
			if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
				return true, nil
			}
		}
		return false, nil
	}

	conditionError := errors.New("condition check failed")
	conditionWithError := func(_ *corev1.Node) (bool, error) {
		return false, conditionError
	}

	type watchEvent struct {
		eventType watch.EventType
		object    *corev1.Node
	}

	tests := []struct {
		name              string
		initialNodes      []*corev1.Node
		watchEvents       []watchEvent
		condition         Predicate
		advanceTime       time.Duration
		cancelWaitContext bool
		stopManager       bool
		wantErr           error
		wantErrContains   string
		wantChannelClosed bool
		wantStillWaiting  bool
	}{
		{
			name:         "node already ready in cache",
			initialNodes: []*corev1.Node{nodeReady},
			watchEvents:  nil,
			condition:    conditionReady,
			wantErr:      nil,
		},
		{
			name:         "node becomes ready via add event",
			initialNodes: nil,
			watchEvents: []watchEvent{
				{eventType: watch.Added, object: nodeReady},
			},
			condition: conditionReady,
			wantErr:   nil,
		},
		{
			name:         "node becomes ready via update event",
			initialNodes: nil,
			watchEvents: []watchEvent{
				{eventType: watch.Added, object: nodeNotReady},
				{eventType: watch.Modified, object: nodeReady},
			},
			condition: conditionReady,
			wantErr:   nil,
		},
		{
			name:         "context timeout before node ready",
			initialNodes: nil,
			watchEvents: []watchEvent{
				{eventType: watch.Added, object: nodeNotReady},
			},
			condition:   conditionReady,
			advanceTime: 2 * time.Second,
			wantErr:     context.DeadlineExceeded,
		},
		{
			name:         "condition returns error on initial check",
			initialNodes: []*corev1.Node{nodeNotReady},
			condition:    conditionWithError,
			wantErr:      conditionError,
		},
		{
			name:              "context cancelled with node ready in cache returns success",
			initialNodes:      []*corev1.Node{nodeReady},
			condition:         conditionReady,
			cancelWaitContext: true,
			wantErr:           nil,
		},
		{
			name:              "context cancelled without node returns cancelled error",
			initialNodes:      nil,
			condition:         conditionReady,
			cancelWaitContext: true,
			wantErr:           context.Canceled,
		},
		{
			name:         "ignores events for unrelated nodes",
			initialNodes: nil,
			watchEvents: []watchEvent{
				{eventType: watch.Added, object: otherNodeReady},
			},
			condition:        conditionReady,
			wantStillWaiting: true,
		},
		{
			name:              "stop closes tracked channels",
			initialNodes:      nil,
			condition:         conditionReady,
			stopManager:       true,
			wantChannelClosed: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				var initialObjects []runtime.Object
				for _, node := range tt.initialNodes {
					initialObjects = append(initialObjects, node)
				}

				clientSet := fake.NewClientset(initialObjects...)
				watcher := watch.NewFake()

				clientSet.PrependWatchReactor("nodes", k8stest.DefaultWatchReactor(watcher, nil))

				log := logrus.New()
				log.SetLevel(logrus.DebugLevel)

				infMgr := NewManager(log, clientSet, 10*time.Minute)

				ctx, cancel := context.WithCancel(t.Context())
				t.Cleanup(func() {
					cancel()
					watcher.Stop()
				})

				go func() {
					_ = infMgr.Start(ctx)
				}()
				synctest.Wait()

				waitCtx := t.Context()
				var waitCancel context.CancelFunc
				if tt.advanceTime > 0 {
					waitCtx, waitCancel = context.WithTimeout(t.Context(), 1*time.Second)
					t.Cleanup(waitCancel)
				} else if tt.cancelWaitContext {
					waitCtx, waitCancel = context.WithCancel(t.Context())
				}

				var waitErr error
				var channelOpen bool = true
				waitDone := infMgr.GetNodeInformer().Wait(waitCtx, "test-node", tt.condition)

				go func() {
					var ok bool
					waitErr, ok = <-waitDone
					channelOpen = ok
				}()
				synctest.Wait()

				for _, event := range tt.watchEvents {
					switch event.eventType {
					case watch.Added:
						watcher.Add(event.object)
					case watch.Modified:
						watcher.Modify(event.object)
					case watch.Deleted:
						watcher.Delete(event.object)
					}
					synctest.Wait()
				}

				if tt.cancelWaitContext && waitCancel != nil {
					waitCancel()
					synctest.Wait()
				}

				if tt.stopManager {
					cancel()
					watcher.Stop()
					synctest.Wait()
				}

				if tt.advanceTime > 0 {
					time.Sleep(tt.advanceTime)
					synctest.Wait()
				}

				if tt.wantStillWaiting {
					select {
					case <-waitDone:
						t.Fatal("expected wait to still be waiting")
					default:
					}
					return
				}

				if tt.wantChannelClosed {
					require.False(t, channelOpen, "channel should be closed")
					return
				}

				switch {
				case tt.wantErr != nil:
					require.ErrorIs(t, waitErr, tt.wantErr)
				case tt.wantErrContains != "":
					require.Error(t, waitErr)
					require.Contains(t, waitErr.Error(), tt.wantErrContains)
				default:
					require.NoError(t, waitErr)
				}
			})
		})
	}
}

func TestNodeInformer_Wait_DuplicateTracking(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		clientSet := fake.NewClientset()
		watcher := watch.NewFake()

		clientSet.PrependWatchReactor("nodes", k8stest.DefaultWatchReactor(watcher, nil))

		log := logrus.New()
		log.SetLevel(logrus.DebugLevel)

		infMgr := NewManager(log, clientSet, 10*time.Minute)

		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(func() {
			cancel()
			watcher.Stop()
		})

		go func() {
			_ = infMgr.Start(ctx)
		}()
		synctest.Wait()

		condition := func(_ *corev1.Node) (bool, error) {
			return false, nil
		}

		// First Wait call
		done1 := infMgr.GetNodeInformer().Wait(t.Context(), "test-node", condition)
		synctest.Wait()

		// Second Wait call for same node should return error
		done2 := infMgr.GetNodeInformer().Wait(t.Context(), "test-node", condition)
		synctest.Wait()

		err := <-done2
		require.Error(t, err)
		require.Contains(t, err.Error(), "already being tracked")

		// First channel should still be waiting
		select {
		case <-done1:
			t.Fatal("first wait should not have completed")
		default:
		}
	})
}

func TestNodeInformer_Wait_ContextCancelledCleanup(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		clientSet := fake.NewClientset()
		watcher := watch.NewFake()

		clientSet.PrependWatchReactor("nodes", k8stest.DefaultWatchReactor(watcher, nil))

		log := logrus.New()
		log.SetLevel(logrus.DebugLevel)

		infMgr := NewManager(log, clientSet, 10*time.Minute)

		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(func() {
			cancel()
			watcher.Stop()
		})

		go func() {
			_ = infMgr.Start(ctx)
		}()
		synctest.Wait()

		condition := func(_ *corev1.Node) (bool, error) {
			return false, nil
		}

		waitCtx, waitCancel := context.WithCancel(t.Context())
		done := infMgr.GetNodeInformer().Wait(waitCtx, "test-node", condition)
		synctest.Wait()

		// Cancel the wait context
		waitCancel()
		synctest.Wait()

		err := <-done
		require.ErrorIs(t, err, context.Canceled)

		// Should be able to wait for the same node again after cleanup
		done2 := infMgr.GetNodeInformer().Wait(t.Context(), "test-node", condition)
		synctest.Wait()

		// Should not get "already being tracked" error
		select {
		case err := <-done2:
			if err != nil {
				require.NotContains(t, err.Error(), "already being tracked")
			}
		default:
			// Still waiting is fine
		}
	})
}

func TestNodeInformer_OnEvent_MultipleNodes(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		clientSet := fake.NewClientset()
		watcher := watch.NewFake()

		clientSet.PrependWatchReactor("nodes", k8stest.DefaultWatchReactor(watcher, nil))

		log := logrus.New()
		log.SetLevel(logrus.DebugLevel)

		infMgr := NewManager(log, clientSet, 10*time.Minute)

		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(func() {
			cancel()
			watcher.Stop()
		})

		go func() {
			_ = infMgr.Start(ctx)
		}()
		synctest.Wait()

		conditionReady := func(node *corev1.Node) (bool, error) {
			for _, cond := range node.Status.Conditions {
				if cond.Type == corev1.NodeReady && cond.Status == corev1.ConditionTrue {
					return true, nil
				}
			}
			return false, nil
		}

		// Wait for two different nodes
		done1 := infMgr.GetNodeInformer().Wait(t.Context(), "node-1", conditionReady)
		done2 := infMgr.GetNodeInformer().Wait(t.Context(), "node-2", conditionReady)
		synctest.Wait()

		// Make node-1 ready
		watcher.Add(&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
			Status: corev1.NodeStatus{
				Conditions: []corev1.NodeCondition{
					{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
				},
			},
		})
		synctest.Wait()

		// node-1 should complete
		var err1 error
		go func() {
			err1 = <-done1
		}()
		synctest.Wait()
		require.NoError(t, err1)

		// node-2 should still be waiting
		select {
		case <-done2:
			t.Fatal("node-2 should still be waiting")
		default:
		}

		// Make node-2 ready
		watcher.Add(&corev1.Node{
			ObjectMeta: metav1.ObjectMeta{Name: "node-2"},
			Status: corev1.NodeStatus{
				Conditions: []corev1.NodeCondition{
					{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
				},
			},
		})
		synctest.Wait()

		// node-2 should complete
		var err2 error
		go func() {
			err2 = <-done2
		}()
		synctest.Wait()
		require.NoError(t, err2)
	})
}
