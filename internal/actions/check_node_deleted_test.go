package actions

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
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
		wantErr bool
	}{
		{
			name: "return error when node is not deleted (nodeID is matching)",
			args: args{
				action: &castai.ClusterAction{
					ID: uuid.New().String(),
					ActionCheckNodeDeleted: &castai.ActionCheckNodeDeleted{
						NodeName:   "node1",
						ProviderId: "providerID",
						NodeID:     "node1",
					},
				},
				tuneFakeObjects: []runtime.Object{
					&v1.Node{
						ObjectMeta: metav1.ObjectMeta{
							Name: "node1",
							Labels: map[string]string{
								castai.LabelNodeID: "node1",
							},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "return error when node is not deleted (provider ID is matching)",
			args: args{
				action: &castai.ClusterAction{
					ID: uuid.New().String(),
					ActionCheckNodeDeleted: &castai.ActionCheckNodeDeleted{
						NodeName:   "node1",
						ProviderId: "providerID",
						NodeID:     "node1",
					},
				},
				tuneFakeObjects: []runtime.Object{
					&v1.Node{
						ObjectMeta: metav1.ObjectMeta{
							Name: "node1",
							Labels: map[string]string{
								castai.LabelNodeID: "node1-not-matching",
							},
						},
						Spec: v1.NodeSpec{
							ProviderID: "providerID",
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "handle check successfully when node is not found",
			args: args{
				action: &castai.ClusterAction{
					ID: uuid.New().String(),
					ActionCheckNodeDeleted: &castai.ActionCheckNodeDeleted{
						NodeName:   "node1",
						ProviderId: "providerID",
						NodeID:     "node1-id",
					},
				},
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
			if err := h.Handle(context.Background(), tt.args.action); (err != nil) != tt.wantErr {
				t.Errorf("Handle() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
