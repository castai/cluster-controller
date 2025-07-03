package actions

import (
	"testing"

	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"context"
	"fmt"
	mock_actions "github.com/castai/cluster-controller/internal/actions/mock"
	"github.com/castai/cluster-controller/internal/castai"
	"github.com/golang/mock/gomock"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"reflect"
)

func Test_isNodeIDProviderIDValid(t *testing.T) {
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
			name: "empty node ID and provider id",
			args: args{
				node: &v1.Node{},
			},
			wantErr: errNodeNotValid,
		},
		{
			name: "node ID matches label",
			args: args{
				node: &v1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							castai.LabelNodeID: "node-id-123",
						},
					},
				},
				nodeID: "node-id-123",
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
		},
		{
			name: "node ID is empty but provider ID matches",
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
				providerID: "provider-id-456",
			},
		},
		{
			name: "node ID does not match label but provider ID empty",
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
			wantErr: errNodeNotValid,
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
			wantErr: errNodeNotValid,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isNodeIDProviderIDValid(tt.args.node, tt.args.nodeID, tt.args.providerID)
			require.Equal(t, tt.wantErr != nil, got != nil, "isNodeIDProviderIDValid() error mismatch", got)
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
		name    string
		args    args
		want    *v1.Node
		wantErr error
	}{
		{
			name:    "empty node and provider IDs",
			wantErr: errAction,
		},
		{
			name: "node not found",
			args: args{
				nodeName: "node-not-found",
				nodeID:   "node-id-123",
				tuneNodeV1Interface: func(m *mock_actions.MockNodeInterface) {
					m.EXPECT().Get(gomock.Any(), "node-not-found", metav1.GetOptions{}).
						Return(nil, k8serrors.NewNotFound(v1.Resource("nodes"), "node-not-found"))
				},
			},
			wantErr: errNodeNotFound,
		},
		{
			name: "node found with not matching node ID",
			args: args{
				nodeName: "node-found",
				nodeID:   "node-id-123",
				tuneNodeV1Interface: func(m *mock_actions.MockNodeInterface) {
					m.EXPECT().Get(gomock.Any(), "node-found", metav1.GetOptions{}).
						Return(&v1.Node{
							ObjectMeta: metav1.ObjectMeta{
								Name: "node-found",
								Labels: map[string]string{
									castai.LabelNodeID: "node-id-456",
								},
							},
						}, nil)
				},
			},
			wantErr: errNodeNotValid,
		},
		{
			name: "k8s node getter return error",
			args: args{
				nodeName: "node-error",
				nodeID:   "node-id-123",
				tuneNodeV1Interface: func(m *mock_actions.MockNodeInterface) {
					m.EXPECT().Get(gomock.Any(), "node-error", metav1.GetOptions{}).
						Return(nil, errInternal)
				},
			},
			wantErr: errInternal,
		},
		{
			name: "node is nill",
			args: args{
				nodeName: "node-nil",
				nodeID:   "node-id-123",
				tuneNodeV1Interface: func(m *mock_actions.MockNodeInterface) {
					m.EXPECT().Get(gomock.Any(), "node-nil", metav1.GetOptions{}).
						Return(nil, nil)
				},
			},
			wantErr: errNodeNotFound,
		},
		{
			name: "node found with matching node ID",
			args: args{
				nodeName: "node-found",
				nodeID:   "node-id-123",
				tuneNodeV1Interface: func(m *mock_actions.MockNodeInterface) {
					m.EXPECT().Get(gomock.Any(), "node-found", metav1.GetOptions{}).
						Return(&v1.Node{
							ObjectMeta: metav1.ObjectMeta{
								Name: "node-found",
								Labels: map[string]string{
									castai.LabelNodeID: "node-id-123",
								},
							},
						}, nil)
				},
			},
			want: &v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: "node-found",
					Labels: map[string]string{
						castai.LabelNodeID: "node-id-123",
					},
				},
			},
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

			got, err := getNodeByIDs(context.Background(), clientSet, tt.args.nodeName, tt.args.nodeID, tt.args.providerID)
			require.ErrorIs(t, err, tt.wantErr)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("getNodeByIDs() got = %v, want %v", got, tt.want)
			}
		})
	}
}
