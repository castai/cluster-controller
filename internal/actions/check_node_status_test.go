package actions

import (
	"context"
	"sync"
	"testing"
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
)

func TestCheckNodeStatusHandler_Handle_Deleted(t *testing.T) {
	t.Parallel()
	type fields struct {
		tuneFakeObjects []runtime.Object
	}
	type args struct {
		action *castai.ClusterAction
	}

	nodeName := "node1"
	nodeID := "node-id-123"
	nodeObject := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: nodeName,
			Labels: map[string]string{
				castai.LabelNodeID: nodeID,
			},
		},
	}

	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr error
	}{
		{
			name:    "action is nil",
			wantErr: errAction,
		},
		{
			name: "return error when node is not deleted",
			fields: fields{
				tuneFakeObjects: []runtime.Object{
					nodeObject,
				},
			},
			args: args{
				action: &castai.ClusterAction{
					ID: uuid.New().String(),
					ActionCheckNodeStatus: &castai.ActionCheckNodeStatus{
						WaitTimeoutSeconds: lo.ToPtr(int32(100)),
						NodeName:           nodeName,
						NodeStatus:         castai.ActionCheckNodeStatus_DELETED,
						NodeID:             nodeID,
					},
				},
			},
			wantErr: errNodeNotDeleted,
		},
		{
			name: "node is deleted, if node with the same name exists but id does not match",
			fields: fields{
				tuneFakeObjects: []runtime.Object{
					nodeObject,
				},
			},
			args: args{
				action: &castai.ClusterAction{
					ID: uuid.New().String(),
					ActionCheckNodeStatus: &castai.ActionCheckNodeStatus{
						NodeName:   nodeName,
						NodeStatus: castai.ActionCheckNodeStatus_DELETED,
						NodeID:     "different-node-id",
					},
				},
			},
		},
		{
			name: "handle check successfully when node is not found",
			args: args{
				action: &castai.ClusterAction{
					ID: uuid.New().String(),
					ActionCheckNodeStatus: &castai.ActionCheckNodeStatus{
						NodeName:   nodeName,
						NodeStatus: castai.ActionCheckNodeStatus_DELETED,
						NodeID:     nodeID,
					},
				},
			},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			clientSet := fake.NewClientset(tt.fields.tuneFakeObjects...)
			log := logrus.New()
			log.SetLevel(logrus.DebugLevel)
			h := NewCheckNodeStatusHandler(
				log, clientSet)
			err := h.Handle(context.Background(), tt.args.action)
			require.ErrorIs(t, err, tt.wantErr, "unexpected error: %v", err)
		})
	}
}

func TestCheckNodeStatusHandler_Handle_Ready(t *testing.T) {
	t.Parallel()
	type tuneFakeObjects struct {
		event  watch.EventType
		object runtime.Object
	}
	type fields struct {
		tuneFakeObjects []tuneFakeObjects
	}
	type args struct {
		action *castai.ClusterAction
	}

	nodeName := "node1"
	nodeID := "node-id-123"
	nodeUID := types.UID(uuid.New().String())
	var nodeObjectNotReady, nodeObjectReady, nodeObjectReadyTainted, node2ObjectReadyAnotherNodeID runtime.Object
	nodeObjectNotReady = &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			UID:  nodeUID,
			Name: nodeName,
			Labels: map[string]string{
				castai.LabelNodeID: nodeID,
			},
		},
		Status: v1.NodeStatus{
			Conditions: []v1.NodeCondition{},
		},
	}
	nodeObjectReadyTainted = &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			UID:  nodeUID,
			Name: nodeName,
			Labels: map[string]string{
				castai.LabelNodeID: nodeID,
			},
		},
		Spec: v1.NodeSpec{
			Taints: []v1.Taint{taintCloudProviderUninitialized},
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

	nodeObjectReady = &v1.Node{
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
	}

	node2ObjectReadyAnotherNodeID = &v1.Node{
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
	}

	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr error
	}{
		{
			name:    "action is nil",
			wantErr: errAction,
		},
		{
			name: "return error when ctx timeout",
			args: args{
				action: &castai.ClusterAction{
					ID: uuid.New().String(),
					ActionCheckNodeStatus: &castai.ActionCheckNodeStatus{
						NodeName:           "node1",
						NodeStatus:         castai.ActionCheckNodeStatus_READY,
						WaitTimeoutSeconds: lo.ToPtr(int32(1)),
					},
				},
			},
			wantErr: context.DeadlineExceeded,
		},
		{
			name: "return error when ctx timeout: node not ready",
			fields: fields{
				tuneFakeObjects: []tuneFakeObjects{
					{
						event:  watch.Modified,
						object: nodeObjectNotReady,
					},
				},
			},
			args: args{
				action: &castai.ClusterAction{
					ID: uuid.New().String(),
					ActionCheckNodeStatus: &castai.ActionCheckNodeStatus{
						NodeName:           nodeName,
						NodeID:             nodeID,
						NodeStatus:         castai.ActionCheckNodeStatus_READY,
						WaitTimeoutSeconds: lo.ToPtr(int32(2)),
					},
				},
			},
			wantErr: context.DeadlineExceeded,
		},
		{
			name: "return error when ctx timeout: node is ready but has different match ID",
			fields: fields{
				tuneFakeObjects: []tuneFakeObjects{
					{
						event:  watch.Modified,
						object: node2ObjectReadyAnotherNodeID,
					},
					{
						event:  watch.Modified,
						object: node2ObjectReadyAnotherNodeID,
					},
				},
			},
			args: args{
				action: &castai.ClusterAction{
					ID: uuid.New().String(),
					ActionCheckNodeStatus: &castai.ActionCheckNodeStatus{
						NodeName:           nodeName,
						NodeID:             nodeID,
						NodeStatus:         castai.ActionCheckNodeStatus_READY,
						WaitTimeoutSeconds: lo.ToPtr(int32(2)),
					},
				},
			},
			wantErr: context.DeadlineExceeded,
		},
		{
			name: "return error when ctx timeout: node is ready but tainted",
			fields: fields{
				tuneFakeObjects: []tuneFakeObjects{
					{
						event:  watch.Modified,
						object: nodeObjectReadyTainted,
					},
					{
						event:  watch.Modified,
						object: node2ObjectReadyAnotherNodeID,
					},
				},
			},
			args: args{
				action: &castai.ClusterAction{
					ID: uuid.New().String(),
					ActionCheckNodeStatus: &castai.ActionCheckNodeStatus{
						NodeName:           nodeName,
						NodeID:             nodeID,
						NodeStatus:         castai.ActionCheckNodeStatus_READY,
						WaitTimeoutSeconds: lo.ToPtr(int32(2)),
					},
				},
			},
			wantErr: context.DeadlineExceeded,
		},
		{
			name: "handle check successfully when node become ready",
			fields: fields{
				tuneFakeObjects: []tuneFakeObjects{
					{
						event:  watch.Modified,
						object: nodeObjectNotReady,
					},
					{
						event:  watch.Modified,
						object: nodeObjectReadyTainted,
					},
					{
						event:  watch.Modified,
						object: nodeObjectReady,
					},
				},
			},
			args: args{
				action: &castai.ClusterAction{
					ID: uuid.New().String(),
					ActionCheckNodeStatus: &castai.ActionCheckNodeStatus{
						NodeName:           nodeName,
						NodeID:             nodeID,
						NodeStatus:         castai.ActionCheckNodeStatus_READY,
						WaitTimeoutSeconds: lo.ToPtr(int32(10)),
					},
				},
			},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			clientSet := fake.NewClientset()
			watcher := watch.NewFake()
			defer watcher.Stop()

			go func() {
				if len(tt.fields.tuneFakeObjects) == 0 {
					return
				}
				watcher.Add(nodeObjectNotReady)
				watcher.Add(node2ObjectReadyAnotherNodeID)
				for _, obj := range tt.fields.tuneFakeObjects {
					watcher.Action(obj.event, obj.object)
				}
			}()
			clientSet.PrependWatchReactor("nodes", k8stest.DefaultWatchReactor(watcher, nil))

			log := logrus.New()
			log.SetLevel(logrus.DebugLevel)
			h := NewCheckNodeStatusHandler(log, clientSet)

			err := h.Handle(context.Background(), tt.args.action)
			require.ErrorIs(t, err, tt.wantErr, "unexpected error: %v", err)
		})
	}
}

func TestCheckStatus_Ready(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)

	t.Run("handle check successfully when node become ready - removed taint", func(t *testing.T) {
		r := require.New(t)
		nodeName := "node1"
		node := &v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: nodeName,
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
				Taints:     []v1.Taint{taintCloudProviderUninitialized},
				ProviderID: "aws:///us-east-1",
			},
		}
		clientset := fake.NewClientset(node)

		h := CheckNodeStatusHandler{
			log:       log,
			clientset: clientset,
		}

		timeout := int32(60)
		action := &castai.ClusterAction{
			ID: uuid.New().String(),
			ActionCheckNodeStatus: &castai.ActionCheckNodeStatus{
				NodeName:           "node1",
				ProviderId:         "aws:///us-east-1",
				NodeStatus:         castai.ActionCheckNodeStatus_READY,
				WaitTimeoutSeconds: &timeout,
			},
		}

		var wg sync.WaitGroup
		wg.Add(2)
		var err error
		go func() {
			err = h.Handle(context.Background(), action)
			wg.Done()
		}()

		go func() {
			time.Sleep(1 * time.Second)
			node.Spec.Taints = []v1.Taint{}
			_, _ = clientset.CoreV1().Nodes().Update(context.Background(), node, metav1.UpdateOptions{})
			wg.Done()
		}()
		wg.Wait()

		r.NoError(err)
	})
}
