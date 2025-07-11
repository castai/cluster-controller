package actions

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/castai/cluster-controller/internal/castai"
)

func TestCheckNodeDeletedHandler_Handle(t *testing.T) {
	t.Parallel()
	type fields struct {
		cfg checkNodeDeletedConfig
	}
	type args struct {
		action          *castai.ClusterAction
		tuneFakeObjects []runtime.Object
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr error
	}{
		{
			name: "wrong action type",
			args: args{
				action: &castai.ClusterAction{},
			},
			wantErr: errAction,
		},
		{
			name: "empty node name",
			args: args{
				action: newActionCheckNodeDeleted("", nodeID, providerID),
			},
			wantErr: errAction,
		},
		{
			name: "nodeID is not matching",
			args: args{
				action: newActionCheckNodeDeleted(nodeName, nodeID, providerID),
				tuneFakeObjects: []runtime.Object{
					&v1.Node{
						ObjectMeta: metav1.ObjectMeta{
							Name: nodeName,
							Labels: map[string]string{
								castai.LabelNodeID: "another-node-id",
							},
						},
						Spec: v1.NodeSpec{
							ProviderID: providerID,
						},
					},
				},
			},
		},
		{
			name: "provider is not matching",
			args: args{
				action: newActionCheckNodeDeleted(nodeName, nodeID, providerID),
				tuneFakeObjects: []runtime.Object{
					&v1.Node{
						ObjectMeta: metav1.ObjectMeta{
							Name: nodeName,
							Labels: map[string]string{
								castai.LabelNodeID: nodeID,
							},
						},
						Spec: v1.NodeSpec{
							ProviderID: "another-provider-id",
						},
					},
				},
			},
		},
		{
			name: "provider id of Node is empty but nodeID matches",
			args: args{
				action: newActionCheckNodeDeleted(nodeName, nodeID, providerID),
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
				action: newActionCheckNodeDeleted(nodeName, nodeID, ""),
				tuneFakeObjects: []runtime.Object{
					&v1.Node{
						ObjectMeta: metav1.ObjectMeta{
							Name: nodeName,
							Labels: map[string]string{
								castai.LabelNodeID: nodeID,
							},
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
			name: "node id at label is empty but provider ID matches",
			args: args{
				action: newActionCheckNodeDeleted(nodeName, nodeID, providerID),
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
				action: newActionCheckNodeDeleted(nodeName, "", providerID),
				tuneFakeObjects: []runtime.Object{
					&v1.Node{
						ObjectMeta: metav1.ObjectMeta{
							Name: nodeName,
							Labels: map[string]string{
								castai.LabelNodeID: nodeID,
							},
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
			name: "node found and matches IDs",
			args: args{
				action: newActionCheckNodeDeleted(nodeName, nodeID, providerID),
				tuneFakeObjects: []runtime.Object{
					&v1.Node{
						ObjectMeta: metav1.ObjectMeta{
							Name: nodeName,
							Labels: map[string]string{
								castai.LabelNodeID: nodeID,
							},
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
			name: "handle check successfully when node is not found",
			args: args{
				action: newActionCheckNodeDeleted(nodeName, nodeID, providerID),
			},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			clientSet := fake.NewClientset(tt.args.tuneFakeObjects...)
			h := &CheckNodeDeletedHandler{
				log:       logrus.New(),
				clientset: clientSet,
				cfg:       tt.fields.cfg,
			}
			err := h.Handle(context.Background(), tt.args.action)
			require.Equal(t, tt.wantErr != nil, err != nil, "Handle() error = %v, wantErr %v", err, tt.wantErr)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr, "Handle() error mismatch")
			}
		})
	}
}

func newActionCheckNodeDeleted(nodeName, nodeID, providerID string) *castai.ClusterAction {
	return &castai.ClusterAction{
		ID: uuid.New().String(),
		ActionCheckNodeDeleted: &castai.ActionCheckNodeDeleted{
			NodeName:   nodeName,
			ProviderId: providerID,
			NodeID:     nodeID,
		},
	}
}
