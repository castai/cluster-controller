package actions

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"
	"github.com/samber/lo"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	k8stest "k8s.io/client-go/testing"

	"github.com/castai/cluster-controller/internal/castai"
	"github.com/castai/cluster-controller/internal/informer"
)

func TestCheckNodeStatusInformerHandler_Handle_Validation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		action          *castai.ClusterAction
		wantErr         error
		wantErrContains string
	}{
		{
			name:    "action is nil",
			action:  nil,
			wantErr: errAction,
		},
		{
			name: "wrong action type",
			action: &castai.ClusterAction{
				ActionDrainNode: &castai.ActionDrainNode{},
			},
			wantErr: errAction,
		},
		{
			name:    "empty node name",
			action:  newActionCheckNodeStatus("", nodeID, providerID, castai.ActionCheckNodeStatus_READY, lo.ToPtr(int32(1))),
			wantErr: errAction,
		},
		{
			name:    "empty node ID and provider ID",
			action:  newActionCheckNodeStatus(nodeName, "", "", castai.ActionCheckNodeStatus_READY, lo.ToPtr(int32(1))),
			wantErr: errAction,
		},
		{
			name:            "unknown status returns error",
			action:          newActionCheckNodeStatus(nodeName, nodeID, providerID, "invalid_status", lo.ToPtr(int32(1))),
			wantErrContains: "unknown status to check provided node",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				clientSet := fake.NewClientset()
				log := logrus.New()
				log.SetLevel(logrus.DebugLevel)

				infMgr := informer.NewManager(log, clientSet, 10*time.Minute, informer.EnableNodeInformer())

				ctx, cancel := context.WithCancel(t.Context())
				t.Cleanup(cancel)

				go func() {
					_ = infMgr.Start(ctx)
				}()
				synctest.Wait()

				h := NewCheckNodeStatusInformerHandler(log, clientSet, infMgr.GetNodeInformer())
				err := h.Handle(t.Context(), tt.action)

				switch {
				case tt.wantErr != nil:
					require.ErrorIs(t, err, tt.wantErr)
				case tt.wantErrContains != "":
					require.Error(t, err)
					require.Contains(t, err.Error(), tt.wantErrContains)
				default:
					require.NoError(t, err)
				}
			})
		})
	}
}

func TestCheckNodeStatusInformerHandler_Handle_Ready(t *testing.T) {
	t.Parallel()

	nodeUID := types.UID(uuid.New().String())

	nodeObjectNotReady := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			UID:  nodeUID,
			Name: nodeName,
			Labels: map[string]string{
				castai.LabelNodeID: nodeID,
			},
		},
		Spec: v1.NodeSpec{
			ProviderID: providerID,
		},
		Status: v1.NodeStatus{
			Conditions: []v1.NodeCondition{},
		},
	}

	nodeObjectReady := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			UID:  nodeUID,
			Name: nodeName,
			Labels: map[string]string{
				castai.LabelNodeID: nodeID,
			},
		},
		Status: v1.NodeStatus{
			Conditions: []v1.NodeCondition{
				{
					Type:   v1.NodeReady,
					Status: v1.ConditionTrue,
				},
			},
		},
		Spec: v1.NodeSpec{
			ProviderID: providerID,
		},
	}

	nodeObjectReadyTainted := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			UID:  nodeUID,
			Name: nodeName,
			Labels: map[string]string{
				castai.LabelNodeID: nodeID,
			},
		},
		Spec: v1.NodeSpec{
			Taints:     []v1.Taint{taintCloudProviderUninitialized},
			ProviderID: providerID,
		},
		Status: v1.NodeStatus{
			Conditions: []v1.NodeCondition{
				{
					Type:   v1.NodeReady,
					Status: v1.ConditionTrue,
				},
			},
		},
	}

	nodeObjectReadyAnotherID := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			UID:  types.UID(uuid.New().String()),
			Name: nodeName,
			Labels: map[string]string{
				castai.LabelNodeID: "another-node-id",
			},
		},
		Status: v1.NodeStatus{
			Conditions: []v1.NodeCondition{
				{
					Type:   v1.NodeReady,
					Status: v1.ConditionTrue,
				},
			},
		},
		Spec: v1.NodeSpec{
			ProviderID: providerID,
		},
	}

	type watchEvent struct {
		eventType watch.EventType
		object    *v1.Node
	}

	tests := []struct {
		name            string
		action          *castai.ClusterAction
		initialNodes    []*v1.Node
		watchEvents     []watchEvent
		advanceTime     time.Duration
		wantErr         error
		wantErrContains string
	}{
		{
			name:         "node already ready in cache",
			action:       newActionCheckNodeStatus(nodeName, nodeID, providerID, castai.ActionCheckNodeStatus_READY, lo.ToPtr(int32(5))),
			initialNodes: []*v1.Node{nodeObjectReady},
			watchEvents:  nil,
			wantErr:      nil,
		},
		{
			name:         "node becomes ready via update event",
			action:       newActionCheckNodeStatus(nodeName, nodeID, providerID, castai.ActionCheckNodeStatus_READY, lo.ToPtr(int32(5))),
			initialNodes: nil,
			watchEvents: []watchEvent{
				{eventType: watch.Added, object: nodeObjectNotReady},
				{eventType: watch.Modified, object: nodeObjectReady},
			},
			wantErr: nil,
		},
		{
			name:         "node becomes ready via add event",
			action:       newActionCheckNodeStatus(nodeName, nodeID, providerID, castai.ActionCheckNodeStatus_READY, lo.ToPtr(int32(5))),
			initialNodes: nil,
			watchEvents: []watchEvent{
				{eventType: watch.Added, object: nodeObjectReady},
			},
			wantErr: nil,
		},
		{
			name:         "node becomes ready after taint removed",
			action:       newActionCheckNodeStatus(nodeName, nodeID, providerID, castai.ActionCheckNodeStatus_READY, lo.ToPtr(int32(5))),
			initialNodes: nil,
			watchEvents: []watchEvent{
				{eventType: watch.Added, object: nodeObjectReadyTainted},
				{eventType: watch.Modified, object: nodeObjectReady},
			},
			wantErr: nil,
		},
		{
			name:         "succeeds with empty provider ID in request",
			action:       newActionCheckNodeStatus(nodeName, nodeID, "", castai.ActionCheckNodeStatus_READY, lo.ToPtr(int32(5))),
			initialNodes: nil,
			watchEvents: []watchEvent{
				{eventType: watch.Added, object: nodeObjectReady},
			},
			wantErr: nil,
		},
		{
			name:         "timeout when node never becomes ready",
			action:       newActionCheckNodeStatus(nodeName, nodeID, providerID, castai.ActionCheckNodeStatus_READY, lo.ToPtr(int32(1))),
			initialNodes: nil,
			watchEvents: []watchEvent{
				{eventType: watch.Added, object: nodeObjectNotReady},
			},
			advanceTime: 2 * time.Second,
			wantErr:     context.DeadlineExceeded,
		},
		{
			name:         "timeout when node is ready but has different ID",
			action:       newActionCheckNodeStatus(nodeName, nodeID, providerID, castai.ActionCheckNodeStatus_READY, lo.ToPtr(int32(1))),
			initialNodes: nil,
			watchEvents: []watchEvent{
				{eventType: watch.Added, object: nodeObjectReadyAnotherID},
			},
			advanceTime: 2 * time.Second,
			wantErr:     context.DeadlineExceeded,
		},
		{
			name:         "timeout when node stays tainted",
			action:       newActionCheckNodeStatus(nodeName, nodeID, providerID, castai.ActionCheckNodeStatus_READY, lo.ToPtr(int32(1))),
			initialNodes: nil,
			watchEvents: []watchEvent{
				{eventType: watch.Added, object: nodeObjectReadyTainted},
			},
			advanceTime: 2 * time.Second,
			wantErr:     context.DeadlineExceeded,
		},
		{
			name:         "timeout when no events received",
			action:       newActionCheckNodeStatus(nodeName, nodeID, providerID, castai.ActionCheckNodeStatus_READY, lo.ToPtr(int32(1))),
			initialNodes: nil,
			watchEvents:  nil,
			advanceTime:  2 * time.Second,
			wantErr:      context.DeadlineExceeded,
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

				infMgr := informer.NewManager(log, clientSet, 10*time.Minute, informer.EnableNodeInformer())

				ctx, cancel := context.WithCancel(t.Context())
				t.Cleanup(func() {
					cancel()
					watcher.Stop()
				})

				go func() {
					_ = infMgr.Start(ctx)
				}()
				synctest.Wait()

				h := NewCheckNodeStatusInformerHandler(log, clientSet, infMgr.GetNodeInformer())

				var handleErr error
				go func() {
					handleErr = h.Handle(t.Context(), tt.action)
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

				if tt.advanceTime > 0 {
					time.Sleep(tt.advanceTime)
					synctest.Wait()
				}

				switch {
				case tt.wantErr != nil:
					require.ErrorIs(t, handleErr, tt.wantErr)
				case tt.wantErrContains != "":
					require.Error(t, handleErr)
					require.Contains(t, handleErr.Error(), tt.wantErrContains)
				default:
					require.NoError(t, handleErr)
				}
			})
		})
	}
}
