package actions

import (
	"context"
	"fmt"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mock_actions "github.com/castai/cluster-controller/internal/actions/mock"
	"github.com/castai/cluster-controller/internal/castai"
)

func Test_isNodeIDProviderIDValid(t *testing.T) {
	t.Parallel()

	type args struct {
		node       *v1.Node
		nodeID     string
		providerID string
	}
	tests := []struct {
		name    string
		args    args
		wantErr error
	}{
		{
			name: "empty node ID and provider id in request",
			args: args{
				node:       &v1.Node{},
				providerID: "",
				nodeID:     "",
			},
			wantErr: errAction,
		},
		{
			name: "request node ID is empty but node id exists in node labels",
			args: args{
				node: &v1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							castai.LabelNodeID: "node-id-123-existed",
						},
					},
					Spec: v1.NodeSpec{
						ProviderID: "provider-id-456",
					},
				},
				providerID: "provider-id-456",
			},
			wantErr: errNodeDoesNotMatch,
		},
		{
			name: "request and labels node ID are empty but provider ID matches",
			args: args{
				node: &v1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							castai.LabelNodeID: "",
						},
					},
					Spec: v1.NodeSpec{
						ProviderID: "provider-id-456",
					},
				},
				providerID: "provider-id-456",
			},
		},
		{
			name: "request node ID is empty and no labels but provider ID matches",
			args: args{
				node: &v1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{},
					},
					Spec: v1.NodeSpec{
						ProviderID: "provider-id-456",
					},
				},
				providerID: "provider-id-456",
			},
		},
		{
			name: "node ID and provider ID matches",
			args: args{
				node: &v1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							castai.LabelNodeID: "node-id-123",
						},
					},
					Spec: v1.NodeSpec{
						ProviderID: "provider-id-456",
					},
				},
				nodeID:     "node-id-123",
				providerID: "provider-id-456",
			},
		},
		{
			name: "node ID does not match label but provider ID matches",
			args: args{
				node: &v1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							castai.LabelNodeID: "node-id-123-not-matching",
						},
					},
					Spec: v1.NodeSpec{
						ProviderID: "provider-id-456",
					},
				},
				nodeID:     "node-id-123",
				providerID: "provider-id-456",
			},
			wantErr: errNodeDoesNotMatch,
		},
		{
			name: "node ID does not match label, provider ID empty",
			args: args{
				node: &v1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							castai.LabelNodeID: "node-id-123-not-matching",
						},
					},
					Spec: v1.NodeSpec{
						ProviderID: "provider-id-456",
					},
				},
				nodeID:     "node-id-123",
				providerID: "",
			},
			wantErr: errNodeDoesNotMatch,
		},
		{
			name: "node ID and provider ID do not match",
			args: args{
				node: &v1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							castai.LabelNodeID: "node-id-123-not-matching",
						},
					},
					Spec: v1.NodeSpec{
						ProviderID: "provider-id-456-not-matching",
					},
				},
				nodeID:     "node-id-123",
				providerID: "provider-id-456",
			},
			wantErr: errNodeDoesNotMatch,
		},
		{
			name: "node ID is match and request provider ID is empty",
			args: args{
				node: &v1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							castai.LabelNodeID: "node-id-123",
						},
					},
					Spec: v1.NodeSpec{
						ProviderID: "provider-id-456-not-matching",
					},
				},
				nodeID:     "node-id-123",
				providerID: "",
			},
		},
		{
			name: "node ID is match and provider ID is empty in Node spec",
			args: args{
				node: &v1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							castai.LabelNodeID: "node-id-123",
						},
					},
					Spec: v1.NodeSpec{
						ProviderID: "",
					},
				},
				nodeID:     "node-id-123",
				providerID: "provider-id-456",
			},
		},
	}
	for _, tt := range tests {
		tt := tt // capture range variable
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := isNodeIDProviderIDValid(tt.args.node, tt.args.nodeID, tt.args.providerID, logrus.New())
			require.Equal(t, tt.wantErr != nil, got != nil, "error mismatch", got)
			require.ErrorIs(t, got, tt.wantErr)
		})
	}
}

func Test_getNodeByIDs(t *testing.T) {
	t.Parallel()

	errInternal := k8serrors.NewInternalError(fmt.Errorf("internal error"))
	type args struct {
		tuneNodeV1Interface func(m *mock_actions.MockNodeInterface)
		nodeName            string
		nodeID              string
		providerID          string
	}
	tests := []struct {
		name     string
		args     args
		wantNode bool
		wantErr  error
	}{
		{
			name:    "empty node and provider IDs",
			wantErr: errAction,
		},
		{
			name: "node not found",
			args: args{
				nodeName: nodeName,
				nodeID:   nodeID,
				tuneNodeV1Interface: func(m *mock_actions.MockNodeInterface) {
					m.EXPECT().Get(gomock.Any(), nodeName, metav1.GetOptions{}).
						Return(nil, k8serrors.NewNotFound(v1.Resource("nodes"), nodeName))
				},
			},
			wantErr: errNodeNotFound,
		},
		{
			name: "not matching node ID",
			args: args{
				nodeName:   nodeName,
				nodeID:     nodeID,
				providerID: providerID,
				tuneNodeV1Interface: func(m *mock_actions.MockNodeInterface) {
					m.EXPECT().Get(gomock.Any(), nodeName, metav1.GetOptions{}).
						Return(&v1.Node{
							ObjectMeta: metav1.ObjectMeta{
								Name: nodeName,
								Labels: map[string]string{
									castai.LabelNodeID: "another-node-id",
								},
							},
							Spec: v1.NodeSpec{
								ProviderID: providerID,
							},
						}, nil)
				},
			},
			wantErr: errNodeDoesNotMatch,
		},
		{
			name: "node id at request is empty but provider ID matches",
			args: args{
				nodeName:   nodeName,
				nodeID:     "",
				providerID: providerID,
				tuneNodeV1Interface: func(m *mock_actions.MockNodeInterface) {
					m.EXPECT().Get(gomock.Any(), nodeName, metav1.GetOptions{}).
						Return(&v1.Node{
							ObjectMeta: metav1.ObjectMeta{
								Name: nodeName,
								Labels: map[string]string{
									castai.LabelNodeID: nodeID,
								},
							},
							Spec: v1.NodeSpec{
								ProviderID: providerID,
							},
						}, nil)
				},
			},
			wantNode: true,
		},
		{
			name: "node id at label is empty but provider ID matches",
			args: args{
				nodeName:   nodeName,
				nodeID:     nodeID,
				providerID: providerID,
				tuneNodeV1Interface: func(m *mock_actions.MockNodeInterface) {
					m.EXPECT().Get(gomock.Any(), nodeName, metav1.GetOptions{}).
						Return(&v1.Node{
							ObjectMeta: metav1.ObjectMeta{
								Name:   nodeName,
								Labels: map[string]string{},
							},
							Spec: v1.NodeSpec{
								ProviderID: providerID,
							},
						}, nil)
				},
			},
			wantNode: true,
		},
		{
			name: "provider id at Node object is empty but node ID matches",
			args: args{
				nodeName:   nodeName,
				nodeID:     nodeID,
				providerID: providerID,
				tuneNodeV1Interface: func(m *mock_actions.MockNodeInterface) {
					m.EXPECT().Get(gomock.Any(), nodeName, metav1.GetOptions{}).
						Return(&v1.Node{
							ObjectMeta: metav1.ObjectMeta{
								Name: nodeName,
								Labels: map[string]string{
									castai.LabelNodeID: nodeID,
								},
							},
							Spec: v1.NodeSpec{
								ProviderID: "",
							},
						}, nil)
				},
			},
			wantNode: true,
		},
		{
			name: "provider id at request is empty but node ID matches",
			args: args{
				nodeName:   nodeName,
				nodeID:     nodeID,
				providerID: "",
				tuneNodeV1Interface: func(m *mock_actions.MockNodeInterface) {
					m.EXPECT().Get(gomock.Any(), nodeName, metav1.GetOptions{}).
						Return(&v1.Node{
							ObjectMeta: metav1.ObjectMeta{
								Name: nodeName,
								Labels: map[string]string{
									castai.LabelNodeID: nodeID,
								},
							},
							Spec: v1.NodeSpec{
								ProviderID: providerID,
							},
						}, nil)
				},
			},
			wantNode: true,
		},
		{
			name: "k8s node getter return error",
			args: args{
				nodeName: nodeName,
				nodeID:   nodeID,
				tuneNodeV1Interface: func(m *mock_actions.MockNodeInterface) {
					m.EXPECT().Get(gomock.Any(), nodeName, metav1.GetOptions{}).
						Return(nil, errInternal)
				},
			},
			wantErr: errInternal,
		},
		{
			name: "node is nill",
			args: args{
				nodeName: nodeName,
				nodeID:   nodeID,
				tuneNodeV1Interface: func(m *mock_actions.MockNodeInterface) {
					m.EXPECT().Get(gomock.Any(), nodeName, metav1.GetOptions{}).
						Return(nil, nil)
				},
			},
			wantErr: errNodeNotFound,
		},
		{
			name: "node found with matching IDs",
			args: args{
				nodeName:   nodeName,
				nodeID:     nodeID,
				providerID: providerID,
				tuneNodeV1Interface: func(m *mock_actions.MockNodeInterface) {
					m.EXPECT().Get(gomock.Any(), nodeName, metav1.GetOptions{}).
						Return(&v1.Node{
							ObjectMeta: metav1.ObjectMeta{
								Name: nodeName,
								Labels: map[string]string{
									castai.LabelNodeID: nodeID,
								},
							},
							Spec: v1.NodeSpec{
								ProviderID: providerID,
							},
						}, nil)
				},
			},
			wantNode: true,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			clientSet := mock_actions.NewMockNodeInterface(ctrl)
			if tt.args.tuneNodeV1Interface != nil {
				tt.args.tuneNodeV1Interface(clientSet)
			}

			got, err := getNodeByIDs(context.Background(), clientSet, tt.args.nodeName, tt.args.nodeID, tt.args.providerID, logrus.New())
			require.ErrorIs(t, err, tt.wantErr)
			require.Equal(t, tt.wantNode, got != nil, "getNodeByIDs() does not expect node")
		})
	}
}
