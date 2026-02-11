package k8s

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	mock_actions "github.com/castai/cluster-controller/internal/actions/mock"
	"github.com/castai/cluster-controller/internal/castai"
)

const (
	nodeName   = "node1"
	nodeID     = "node-id"
	providerID = "aws:///us-east-1"
	podName    = "pod1"
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
			wantErr: ErrAction,
		},
		{
			name: "request node ID is empty but node id exists in node labels and provider ID matches",
			args: args{
				node: &v1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							castai.LabelNodeID: nodeID,
						},
					},
					Spec: v1.NodeSpec{
						ProviderID: providerID,
					},
				},
				providerID: providerID,
				nodeID:     "",
			},
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
						ProviderID: providerID,
					},
				},
				providerID: providerID,
				nodeID:     "",
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
						ProviderID: providerID,
					},
				},
				providerID: providerID,
				nodeID:     "",
			},
		},
		{
			name: "node ID and provider ID are empty at Node spec",
			args: args{
				node: &v1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{},
					},
					Spec: v1.NodeSpec{
						ProviderID: "",
					},
				},
				nodeID:     nodeID,
				providerID: providerID,
			},
			wantErr: ErrNodeDoesNotMatch,
		},
		{
			name: "node ID is empty at Node spec and Provider is matching",
			args: args{
				node: &v1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{},
					},
					Spec: v1.NodeSpec{
						ProviderID: providerID,
					},
				},
				nodeID:     nodeID,
				providerID: providerID,
			},
		},
		{
			name: "provider ID is empty at Node spec and NodeID is matching",
			args: args{
				node: &v1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							castai.LabelNodeID: nodeID,
						},
					},
					Spec: v1.NodeSpec{
						ProviderID: "",
					},
				},
				nodeID:     nodeID,
				providerID: providerID,
			},
		},
		{
			name: "node ID and provider ID matches",
			args: args{
				node: &v1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							castai.LabelNodeID: nodeID,
						},
					},
					Spec: v1.NodeSpec{
						ProviderID: providerID,
					},
				},
				nodeID:     nodeID,
				providerID: providerID,
			},
		},
		{
			name: "node ID and provider ID matches - different casing",
			args: args{
				node: &v1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							castai.LabelNodeID: nodeID + "DIFFERENT_CASE",
						},
					},
					Spec: v1.NodeSpec{
						ProviderID: providerID + "DIFFERENT_CASE",
					},
				},
				nodeID:     nodeID + "different_case",
				providerID: providerID + "different_case",
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
						ProviderID: providerID,
					},
				},
				nodeID:     nodeID,
				providerID: providerID,
			},
			wantErr: ErrNodeDoesNotMatch,
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
						ProviderID: providerID,
					},
				},
				nodeID:     nodeID,
				providerID: "",
			},
			wantErr: ErrNodeDoesNotMatch,
		},
		{
			name: "node ID is match and request provider ID is empty",
			args: args{
				node: &v1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							castai.LabelNodeID: nodeID,
						},
					},
					Spec: v1.NodeSpec{
						ProviderID: providerID,
					},
				},
				nodeID:     nodeID,
				providerID: "",
			},
		},
		{
			name: "node ID is match and provider ID is empty in Node spec",
			args: args{
				node: &v1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Labels: map[string]string{
							castai.LabelNodeID: nodeID,
						},
					},
					Spec: v1.NodeSpec{
						ProviderID: "",
					},
				},
				nodeID:     nodeID,
				providerID: providerID,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := IsNodeIDProviderIDValid(tt.args.node, tt.args.nodeID, tt.args.providerID)
			require.Equal(t, tt.wantErr != nil, got != nil, "error mismatch", got)
			require.ErrorIs(t, got, tt.wantErr)
		})
	}
}

func TestClient_GetNodeByIDs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		existNode  *v1.Node
		nodeName   string
		nodeID     string
		providerID string
		wantNode   bool
		wantErr    error
	}{
		{
			name:    "empty node and provider IDs",
			wantErr: ErrAction,
		},
		{
			name:      "node not found",
			existNode: nil,
			nodeName:  nodeName,
			nodeID:    nodeID,
			wantErr:   ErrNodeNotFound,
		},
		{
			name: "not matching node ID",
			existNode: &v1.Node{
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
			nodeName:   nodeName,
			nodeID:     nodeID,
			providerID: providerID,
			wantErr:    ErrNodeDoesNotMatch,
		},
		{
			name: "node found with matching IDs",
			existNode: &v1.Node{
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
			nodeName:   nodeName,
			nodeID:     nodeID,
			providerID: providerID,
			wantNode:   true,
		},
		{
			name: "node ID matches, provider ID empty in request",
			existNode: &v1.Node{
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
			nodeName:   nodeName,
			nodeID:     nodeID,
			providerID: "",
			wantNode:   true,
		},
		{
			name: "provider ID matches, node ID empty in request",
			existNode: &v1.Node{
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
			nodeName:   nodeName,
			nodeID:     "",
			providerID: providerID,
			wantNode:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var clientset kubernetes.Interface
			if tt.existNode != nil {
				clientset = fake.NewClientset(tt.existNode)
			} else {
				clientset = fake.NewClientset()
			}

			client := NewClient(clientset, logrus.New())
			got, err := client.GetNodeByIDs(context.Background(), tt.nodeName, tt.nodeID, tt.providerID)
			require.ErrorIs(t, err, tt.wantErr)
			require.Equal(t, tt.wantNode, got != nil, "Client.GetNodeByIDs() does not expect node")
		})
	}
}

func TestClient_ExecuteBatchPodActions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		pods              []*v1.Pod
		action            func(context.Context, v1.Pod) error
		actionName        string
		wantSuccessCount  int
		wantFailureCount  int
		wantFailureAction string
	}{
		{
			name:             "empty pod list",
			pods:             []*v1.Pod{},
			action:           func(ctx context.Context, pod v1.Pod) error { return nil },
			actionName:       "test-action",
			wantSuccessCount: 0,
			wantFailureCount: 0,
		},
		{
			name: "all pods succeed",
			pods: []*v1.Pod{
				{ObjectMeta: metav1.ObjectMeta{Name: "pod1", Namespace: "default"}},
				{ObjectMeta: metav1.ObjectMeta{Name: "pod2", Namespace: "default"}},
				{ObjectMeta: metav1.ObjectMeta{Name: "pod3", Namespace: "default"}},
			},
			action:           func(ctx context.Context, pod v1.Pod) error { return nil },
			actionName:       "evict-pod",
			wantSuccessCount: 3,
			wantFailureCount: 0,
		},
		{
			name: "all pods fail",
			pods: []*v1.Pod{
				{ObjectMeta: metav1.ObjectMeta{Name: "pod1", Namespace: "default"}},
				{ObjectMeta: metav1.ObjectMeta{Name: "pod2", Namespace: "default"}},
			},
			action:            func(ctx context.Context, pod v1.Pod) error { return fmt.Errorf("action failed") },
			actionName:        "delete-pod",
			wantSuccessCount:  0,
			wantFailureCount:  2,
			wantFailureAction: "delete-pod",
		},
		{
			name: "mixed success and failure",
			pods: []*v1.Pod{
				{ObjectMeta: metav1.ObjectMeta{Name: "pod1", Namespace: "default"}},
				{ObjectMeta: metav1.ObjectMeta{Name: "pod-fail", Namespace: "default"}},
				{ObjectMeta: metav1.ObjectMeta{Name: "pod2", Namespace: "default"}},
			},
			action: func(ctx context.Context, pod v1.Pod) error {
				if pod.Name == "pod-fail" {
					return fmt.Errorf("specific pod failed")
				}
				return nil
			},
			actionName:        "test-action",
			wantSuccessCount:  2,
			wantFailureCount:  1,
			wantFailureAction: "test-action",
		},
		{
			name: "default action name when empty",
			pods: []*v1.Pod{
				{ObjectMeta: metav1.ObjectMeta{Name: "pod1", Namespace: "default"}},
			},
			action:            func(ctx context.Context, pod v1.Pod) error { return fmt.Errorf("fail") },
			actionName:        "",
			wantSuccessCount:  0,
			wantFailureCount:  1,
			wantFailureAction: "unspecified",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client := NewClient(nil, logrus.New())
			successful, failed := client.ExecuteBatchPodActions(context.Background(), tt.pods, tt.action, tt.actionName)

			require.Len(t, successful, tt.wantSuccessCount, "unexpected success count")
			require.Len(t, failed, tt.wantFailureCount, "unexpected failure count")

			if tt.wantFailureCount > 0 && tt.wantFailureAction != "" {
				for _, f := range failed {
					require.Equal(t, tt.wantFailureAction, f.ActionName)
					require.NotNil(t, f.Pod)
					require.NotNil(t, f.Err)
				}
			}
		})
	}
}

func TestClient_Accessors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
	}{
		{
			name: "accessors return correct values",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			mockClientset := mock_actions.NewMockInterface(ctrl)
			log := logrus.New()

			client := NewClient(mockClientset, log)

			require.Equal(t, mockClientset, client.Clientset())
			require.Equal(t, log, client.Log())
		})
	}
}

func TestClient_PatchNode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		existNode *v1.Node
		changeFn  func(*v1.Node)
		wantErr   bool
		wantLabel string
	}{
		{
			name: "successfully patch node with new label",
			existNode: &v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name:   nodeName,
					Labels: map[string]string{},
				},
			},
			changeFn: func(n *v1.Node) {
				if n.Labels == nil {
					n.Labels = make(map[string]string)
				}
				n.Labels["test-label"] = "test-value"
			},
			wantErr:   false,
			wantLabel: "test-value",
		},
		{
			name: "successfully patch node with taint",
			existNode: &v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
				},
				Spec: v1.NodeSpec{
					Taints: []v1.Taint{},
				},
			},
			changeFn: func(n *v1.Node) {
				n.Spec.Taints = append(n.Spec.Taints, v1.Taint{
					Key:    "node.kubernetes.io/unschedulable",
					Effect: v1.TaintEffectNoSchedule,
				})
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			clientset := fake.NewClientset(tt.existNode)
			client := NewClient(clientset, logrus.New())

			nodeCopy := tt.existNode.DeepCopy()
			err := client.PatchNode(context.Background(), nodeCopy, tt.changeFn)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			if tt.wantLabel != "" {
				updatedNode, err := clientset.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
				require.NoError(t, err)
				require.Equal(t, tt.wantLabel, updatedNode.Labels["test-label"])
			}
		})
	}
}

func TestClient_PatchNodeStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		existNode *v1.Node
		patch     []byte
		wantErr   bool
	}{
		{
			name: "successfully patch node status",
			existNode: &v1.Node{
				ObjectMeta: metav1.ObjectMeta{
					Name: nodeName,
				},
				Status: v1.NodeStatus{
					Phase: v1.NodeRunning,
				},
			},
			patch:   []byte(`{"status":{"phase":"Running"}}`),
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			clientset := fake.NewClientset(tt.existNode)
			client := NewClient(clientset, logrus.New())

			err := client.PatchNodeStatus(context.Background(), nodeName, tt.patch)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestClient_DeletePod(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		existPod          *v1.Pod
		podToDelete       v1.Pod
		deleteRetries     int
		deleteRetryDelay  time.Duration
		wantErr           bool
		wantPodToNotExist bool
	}{
		{
			name: "successfully delete existing pod",
			existPod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod1",
					Namespace: "default",
				},
			},
			podToDelete: v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "pod1",
					Namespace: "default",
				},
			},
			deleteRetries:     3,
			deleteRetryDelay:  10 * time.Millisecond,
			wantErr:           false,
			wantPodToNotExist: true,
		},
		{
			name:     "delete non-existent pod succeeds (not found is ignored)",
			existPod: nil,
			podToDelete: v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "non-existent-pod",
					Namespace: "default",
				},
			},
			deleteRetries:    3,
			deleteRetryDelay: 10 * time.Millisecond,
			wantErr:          false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var clientset kubernetes.Interface
			if tt.existPod != nil {
				clientset = fake.NewClientset(tt.existPod)
			} else {
				clientset = fake.NewClientset()
			}

			client := NewClient(clientset, logrus.New())

			err := client.DeletePod(context.Background(), metav1.DeleteOptions{}, tt.podToDelete, tt.deleteRetries, tt.deleteRetryDelay)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			if tt.wantPodToNotExist {
				_, err := clientset.CoreV1().Pods(tt.podToDelete.Namespace).Get(context.Background(), tt.podToDelete.Name, metav1.GetOptions{})
				require.True(t, k8serrors.IsNotFound(err), "pod should have been deleted")
			}
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
			wantErr: ErrAction,
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
			wantErr: ErrNodeNotFound,
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
			wantErr: ErrNodeDoesNotMatch,
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
			name: "node id is empty at request and Node but provider ID matches",
			args: args{
				nodeName:   nodeName,
				nodeID:     "",
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
			name: "provider id is empty at request and Node but node ID matches",
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
								ProviderID: "",
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
			wantErr: ErrNodeNotFound,
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
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			clientSet := mock_actions.NewMockNodeInterface(ctrl)
			if tt.args.tuneNodeV1Interface != nil {
				tt.args.tuneNodeV1Interface(clientSet)
			}

			got, err := GetNodeByIDs(context.Background(), clientSet, tt.args.nodeName, tt.args.nodeID, tt.args.providerID, logrus.New())
			require.ErrorIs(t, err, tt.wantErr)
			require.Equal(t, tt.wantNode, got != nil, "getNodeByIDs() does not expect node")
		})
	}
}
