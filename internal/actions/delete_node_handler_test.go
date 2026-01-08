package actions

import (
	"context"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfields "k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/castai/cluster-controller/internal/castai"
)

func TestDeleteNodeHandler_Handle(t *testing.T) {
	t.Parallel()
	node := &v1.Node{
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
	type fields struct {
		tuneFakeObjects []runtime.Object
		cfg             deleteNodeConfig
	}
	type args struct {
		action *castai.ClusterAction
	}

	tests := []struct {
		name            string
		fields          fields
		args            args
		wantErr         error
		wantDeletedNode bool
	}{
		{
			name:    "nil",
			args:    args{},
			wantErr: errAction,
		},
		{
			name: "wrong action type",
			args: args{
				action: &castai.ClusterAction{
					ActionDrainNode: &castai.ActionDrainNode{},
				},
			},
			wantErr: errAction,
		},
		{
			name: "empty node name",
			args: args{
				action: newActionDeleteNode("", nodeID, providerID),
			},
			wantErr: errAction,
		},
		{
			name: "empty node ID and provider ID",
			args: args{
				action: newActionDeleteNode(nodeName, "", ""),
			},
			wantErr:         errAction,
			wantDeletedNode: false,
		},
		{
			name: "action with another node id and provider id - node not found",
			args: args{
				action: newActionDeleteNode(nodeName, "another-node-id", "another-provider-id"),
			},
			wantDeletedNode: false,
		},
		{
			name: "action with proper node id and another provider id - node not found",
			args: args{
				action: newActionDeleteNode(nodeName, nodeID, "another-provider-id"),
			},
			wantDeletedNode: false,
		},
		{
			name: "action with another node id and proper provider id - node not found",
			args: args{
				action: newActionDeleteNode(nodeName, nodeID, "another-provider-id"),
			},
			wantDeletedNode: false,
		},
		{
			name: "node not found",
			args: args{
				action: newActionDeleteNode("node-not-found", nodeID, providerID),
			},
			wantDeletedNode: false,
		},
		{
			name: "delete successfully without pods",
			args: args{
				action: newActionDeleteNode(nodeName, nodeID, providerID),
			},
			fields: fields{
				tuneFakeObjects: []runtime.Object{
					node,
				},
			},
			wantDeletedNode: true,
		},
		{
			name: "delete node with pods",
			args: args{
				action: newActionDeleteNode(nodeName, nodeID, providerID),
			},
			fields: fields{
				cfg: deleteNodeConfig{
					podsTerminationWait: 1,
				},
			},
			wantDeletedNode: true,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var clientSet kubernetes.Interface
			if tt.fields.tuneFakeObjects == nil {
				clientSet = setupFakeClientWithNodePodEviction(nodeName, nodeID, providerID, podName)
			} else {
				clientSet = fake.NewClientset(tt.fields.tuneFakeObjects...)
			}
			h := &DeleteNodeHandler{
				log:       logrus.New(),
				clientset: clientSet,
				cfg:       tt.fields.cfg,
				drainCfg:  newDrainNodeConfig(""),
			}
			err := h.Handle(context.Background(), tt.args.action)
			require.Equal(t, tt.wantErr != nil, err != nil, "expected error: %v, got: %v", tt.wantErr, err)
			if tt.wantErr != nil {
				require.ErrorAs(t, err, &tt.wantErr, "Handle() error = %v, wantErr %v", err, tt.wantErr)
			}

			if err != nil {
				return
			}

			_, err = h.clientset.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
			if tt.wantDeletedNode {
				require.True(t, apierrors.IsNotFound(err), "node should be deleted, but got: %v", err)
				pods, err := h.clientset.CoreV1().Pods(metav1.NamespaceAll).List(context.Background(), metav1.ListOptions{
					FieldSelector: k8sfields.SelectorFromSet(k8sfields.Set{"spec.nodeName": nodeName}).String(),
				})
				require.NoError(t, err)
				require.Len(t, pods.Items, 0)
				va, err := h.clientset.StorageV1().VolumeAttachments().List(context.Background(), metav1.ListOptions{
					FieldSelector: k8sfields.SelectorFromSet(k8sfields.Set{}).String(),
				})
				require.NoError(t, err)
				require.Len(t, va.Items, 0)
			} else {
				require.NoError(t, err, "node should not be deleted, but got: %v", err)
			}
		})
	}
}

func newActionDeleteNode(nodeName, nodeID, providerID string) *castai.ClusterAction {
	return &castai.ClusterAction{
		ActionDeleteNode: &castai.ActionDeleteNode{
			NodeName:   nodeName,
			NodeID:     nodeID,
			ProviderId: providerID,
		},
	}
}
