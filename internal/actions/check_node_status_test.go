package actions

import (
	"context"
	"testing"

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

const (
	nodeName   = "node1"
	nodeID     = "node-id"
	providerID = "aws:///us-east-1"
	podName    = "pod1"
)

func TestCheckNodeStatusHandler_Handle_Deleted(t *testing.T) {
	t.Parallel()
	type fields struct {
		tuneFakeObjects []runtime.Object
	}
	type args struct {
		action *castai.ClusterAction
	}

	nodeObject := &v1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: nodeName,
			Labels: map[string]string{
				castai.LabelNodeID: nodeID,
			},
		},
		Spec: v1.NodeSpec{
			ProviderID: providerID,
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
			name: "return error when action data with wrong action type",
			args: args{
				action: &castai.ClusterAction{
					ActionDrainNode: &castai.ActionDrainNode{},
				},
			},
			wantErr: errAction,
		},
		{
			name: "provider is not matching",
			args: args{
				action: newActionCheckNodeStatus(nodeName, nodeID, "another-provider-id", castai.ActionCheckNodeStatus_DELETED, nil),
			},
			fields: fields{
				tuneFakeObjects: []runtime.Object{
					nodeObject,
				},
			},
		},
		{
			name: "provider id of Node is empty but nodeID matches",
			args: args{
				action: newActionCheckNodeStatus(nodeName, nodeID, providerID, castai.ActionCheckNodeStatus_DELETED, nil),
			},
			fields: fields{
				tuneFakeObjects: []runtime.Object{
					&v1.Node{
						ObjectMeta: metav1.ObjectMeta{
							Name: nodeName,
							Labels: map[string]string{
								castai.LabelNodeID: nodeID,
							},
						},
						Spec: v1.NodeSpec{
							ProviderID: "",
						},
					},
				},
			},
			wantErr: errNodeNotDeleted,
		},
		{
			name: "provider id of request is empty but nodeID matches",
			args: args{
				action: newActionCheckNodeStatus(nodeName, nodeID, "", castai.ActionCheckNodeStatus_DELETED, nil),
			},
			fields: fields{
				tuneFakeObjects: []runtime.Object{
					nodeObject,
				},
			},
			wantErr: errNodeNotDeleted,
		},
		{
			name: "node id at label is empty but provider ID matches",
			args: args{
				action: newActionCheckNodeStatus(nodeName, nodeID, providerID, castai.ActionCheckNodeStatus_DELETED, nil),
			},
			fields: fields{
				tuneFakeObjects: []runtime.Object{
					&v1.Node{
						ObjectMeta: metav1.ObjectMeta{
							Name:   nodeName,
							Labels: map[string]string{},
						},
						Spec: v1.NodeSpec{
							ProviderID: providerID,
						},
					},
				},
			},
			wantErr: errNodeNotDeleted,
		},
		{
			name: "node id at request is empty but provider ID matches",
			args: args{
				action: newActionCheckNodeStatus(nodeName, "", providerID, castai.ActionCheckNodeStatus_DELETED, nil),
			},
			fields: fields{
				tuneFakeObjects: []runtime.Object{
					nodeObject,
				},
			},
			wantErr: errNodeNotDeleted,
		},
		{
			name: "node with the same name exists but IDs does not match",
			fields: fields{
				tuneFakeObjects: []runtime.Object{
					nodeObject,
				},
			},
			args: args{
				action: newActionCheckNodeStatus(nodeName, "another-node-id", "another-provider-id", castai.ActionCheckNodeStatus_DELETED, nil),
			},
		},
		{
			name: "node with the same name exists but node id does not match (provider matches)",
			fields: fields{
				tuneFakeObjects: []runtime.Object{
					nodeObject,
				},
			},
			args: args{
				action: newActionCheckNodeStatus(nodeName, "another-node-id", providerID, castai.ActionCheckNodeStatus_DELETED, nil),
			},
		},
		{
			name: "handle check successfully when node is not found",
			args: args{
				action: newActionCheckNodeStatus(nodeName, nodeID, providerID, castai.ActionCheckNodeStatus_DELETED, nil),
			},
		},
		{
			name: "return error when node is not deleted",
			fields: fields{
				tuneFakeObjects: []runtime.Object{
					nodeObject,
				},
			},
			args: args{
				action: newActionCheckNodeStatus(nodeName, nodeID, providerID, castai.ActionCheckNodeStatus_DELETED, nil),
			},
			wantErr: errNodeNotDeleted,
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
		Spec: v1.NodeSpec{
			ProviderID: providerID,
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
		Spec: v1.NodeSpec{
			ProviderID: providerID,
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
		Spec: v1.NodeSpec{
			ProviderID: providerID,
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
			name: "empty node name",
			args: args{
				action: newActionCheckNodeStatus("", nodeID, providerID, castai.ActionCheckNodeStatus_READY, lo.ToPtr(int32(1))),
			},
			wantErr: errAction,
		},
		{
			name: "return error when ctx timeout",
			args: args{
				action: newActionCheckNodeStatus(nodeName, nodeID, providerID, castai.ActionCheckNodeStatus_READY, lo.ToPtr(int32(1))),
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
				action: newActionCheckNodeStatus(nodeName, nodeID, providerID, castai.ActionCheckNodeStatus_READY, lo.ToPtr(int32(2))),
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
				action: newActionCheckNodeStatus(nodeName, nodeID, providerID, castai.ActionCheckNodeStatus_READY, lo.ToPtr(int32(2))),
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
				action: newActionCheckNodeStatus(nodeName, nodeID, providerID, castai.ActionCheckNodeStatus_READY, lo.ToPtr(int32(2))),
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
				action: newActionCheckNodeStatus(nodeName, nodeID, providerID, castai.ActionCheckNodeStatus_READY, lo.ToPtr(int32(10))),
			},
		},
		{
			name: "handle check successfully when node become ready: request with empty provider ID",
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
				action: newActionCheckNodeStatus(nodeName, nodeID, "", castai.ActionCheckNodeStatus_READY, lo.ToPtr(int32(10))),
			},
		},
		{
			name:   "handle check successfully when node become ready - removed taint",
			fields: fields{},
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

func newActionCheckNodeStatus(nodeName, nodeID, providerID string, status castai.ActionCheckNodeStatus_Status, timeout *int32) *castai.ClusterAction {
	return &castai.ClusterAction{
		ID: uuid.New().String(),
		ActionCheckNodeStatus: &castai.ActionCheckNodeStatus{
			NodeName:           nodeName,
			NodeID:             nodeID,
			ProviderId:         providerID,
			NodeStatus:         status,
			WaitTimeoutSeconds: timeout,
		},
	}
}
