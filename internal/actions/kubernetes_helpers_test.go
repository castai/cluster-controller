package actions

import (
	"testing"

	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/castai/cluster-controller/internal/castai"
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
			name: "empty node ID",
			args: args{
				node: &v1.Node{},
			},
			wantErr: errAction,
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
			wantErr: errNodeNotFound,
		},
		{
			name: "node ID  and provider ID do not match",
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
			wantErr: errNodeNotFound,
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
