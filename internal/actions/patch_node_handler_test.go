package actions

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/samber/lo"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/castai/cluster-controller/internal/castai"
	"github.com/castai/cluster-controller/internal/k8s"
)

func TestPatchNodeHandler_Handle(t *testing.T) {
	t.Parallel()
	type fields struct {
		retryTimeout    time.Duration
		tuneFakeObjects []runtime.Object
	}
	type args struct {
		action *castai.ClusterAction
	}
	tests := []struct {
		name              string
		fields            fields
		args              args
		wantErr           error
		wantLabels        map[string]string
		wantAnnotations   map[string]string
		wantTaints        []v1.Taint
		wantCapacity      v1.ResourceList
		wantUnschedulable bool
	}{
		{
			name:    "nil",
			args:    args{},
			wantErr: k8s.ErrAction,
		},
		{
			name: "wrong action type",
			args: args{
				action: &castai.ClusterAction{
					ActionDeleteNode: &castai.ActionDeleteNode{},
				},
			},
			wantErr: k8s.ErrAction,
		},
		{
			name: "labels contain entry with empty key",
			args: args{
				action: newPatchNodeAction(nodeName, nodeID, providerID,
					map[string]string{
						"": "v1",
					},
					nil, nil, nil, nil),
			},
			wantErr: k8s.ErrAction,
		},
		{
			name: "annotations contain entry with empty key",
			args: args{
				action: newPatchNodeAction(nodeName, nodeID, providerID,
					nil,
					map[string]string{
						"": "v1",
					},
					nil, nil, nil),
			},
			wantErr: k8s.ErrAction,
		},
		{
			name: "taints contain entry with empty key",
			args: args{
				action: newPatchNodeAction(nodeName, nodeID, providerID,
					nil, nil,
					[]castai.NodeTaint{
						{
							Key:   "",
							Value: "v1",
						},
					},
					nil, nil),
			},
			wantErr: k8s.ErrAction,
		},
		{
			name: "empty node name",
			args: args{
				action: newPatchNodeAction("", nodeID, providerID,
					nil, nil, nil, nil, nil),
			},
			wantErr: k8s.ErrAction,
		},
		{
			name: "empty node ID and provider ID",
			args: args{
				action: newPatchNodeAction(nodeName, "", "",
					nil, nil, nil, nil, nil),
			},
			wantErr: k8s.ErrAction,
		},
		{
			name: "empty node ID and provider ID at Node", // for Azure legacy nodes it is real case, we consider it as error
			fields: fields{
				retryTimeout: time.Millisecond,
				tuneFakeObjects: []runtime.Object{
					&v1.Node{
						ObjectMeta: metav1.ObjectMeta{
							Name: nodeName,
						},
					},
				},
			},
			args: args{
				action: newPatchNodeAction(nodeName, nodeID, providerID,
					nil, nil, nil, nil, nil),
			},
			wantErr: k8s.ErrNodeDoesNotMatch,
		},
		{
			name: "patch node successfully",
			fields: fields{
				retryTimeout: time.Millisecond,
				tuneFakeObjects: []runtime.Object{
					&v1.Node{
						ObjectMeta: metav1.ObjectMeta{
							Name: nodeName,
							Labels: map[string]string{
								"l1": "v1",
							},
							Annotations: map[string]string{
								"a1": "v1",
							},
						},
						Spec: v1.NodeSpec{
							ProviderID: providerID,
							Taints: []v1.Taint{
								{
									Key:    "t1",
									Value:  "v1",
									Effect: v1.TaintEffectNoSchedule,
								},
								{
									Key:    "t2",
									Value:  "v2",
									Effect: v1.TaintEffectNoSchedule,
								},
							},
						},
					},
				},
			},
			args: args{
				action: newPatchNodeAction(nodeName, nodeID, providerID,
					map[string]string{
						"-l1": "",
						"l2":  "v2",
					},
					map[string]string{
						"-a1": "",
						"a2":  "",
					},
					[]castai.NodeTaint{
						{
							Key:    "t3",
							Value:  "t3",
							Effect: string(v1.TaintEffectNoSchedule),
						},
						{
							Key:    "-t2",
							Value:  "",
							Effect: string(v1.TaintEffectNoSchedule),
						},
					},
					map[v1.ResourceName]resource.Quantity{
						"foo": resource.MustParse("123"),
					},
					nil,
				),
			},
			wantLabels: map[string]string{
				"l2": "v2",
			},
			wantAnnotations: map[string]string{
				"a2": "",
			},
			wantTaints: []v1.Taint{
				{Key: "t1", Value: "v1", Effect: "NoSchedule", TimeAdded: (*metav1.Time)(nil)},
				{Key: "t3", Value: "t3", Effect: "NoSchedule", TimeAdded: (*metav1.Time)(nil)},
			},
			wantCapacity: map[v1.ResourceName]resource.Quantity{
				"foo": resource.MustParse("123"),
			},
		},
		{
			name: "skip patch when node not found",
			fields: fields{
				retryTimeout: time.Millisecond,
				tuneFakeObjects: []runtime.Object{
					&v1.Node{
						ObjectMeta: metav1.ObjectMeta{
							Name: nodeName,
						},
					},
				},
			},
			args: args{
				action: newPatchNodeAction("notFoundNodeName", nodeID, providerID,
					nil, nil, nil, nil, nil),
			},
		},
		{
			name: "cordoning node",
			fields: fields{
				retryTimeout: time.Millisecond,
				tuneFakeObjects: []runtime.Object{
					&v1.Node{
						ObjectMeta: metav1.ObjectMeta{
							Name: nodeName,
							Labels: map[string]string{
								castai.LabelNodeID: nodeID,
							},
						},
						Spec: v1.NodeSpec{
							Unschedulable: false,
						},
					},
				},
			},
			args: args{
				action: newPatchNodeAction(nodeName, nodeID, providerID,
					nil, nil, nil, nil, lo.ToPtr(true)),
			},
			wantLabels: map[string]string{
				castai.LabelNodeID: nodeID,
			},
			wantUnschedulable: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			clientSet := fake.NewClientset(tt.fields.tuneFakeObjects...)
			h := &PatchNodeHandler{
				retryTimeout: tt.fields.retryTimeout,
				log:          logrus.New(),
				clientset:    clientSet,
			}
			err := h.Handle(context.Background(), tt.args.action)
			require.Equal(t, tt.wantErr != nil, err != nil, "Handle() error = %v, wantErr %v", err, tt.wantErr)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr, "Handle() error mismatch")
			} else {
				n, err := clientSet.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
				require.NoError(t, err)

				require.Equal(t, tt.wantLabels, n.Labels, "labels mismatch")
				require.Equal(t, tt.wantAnnotations, n.Annotations, "annotations mismatch")
				require.Equal(t, tt.wantTaints, n.Spec.Taints, "taints mismatch")
				require.Equal(t, tt.wantCapacity, n.Status.Capacity, "capacity mismatch")
				require.Equal(t, tt.wantUnschedulable, n.Spec.Unschedulable, "unschedulable mismatch")
			}
		})
	}
}

func newPatchNodeAction(
	nodeName, nodeID, providerID string,
	labels, annotations map[string]string, taints []castai.NodeTaint, capacity map[v1.ResourceName]resource.Quantity,
	unschedulable *bool,
) *castai.ClusterAction {
	return &castai.ClusterAction{
		ID: uuid.New().String(),
		ActionPatchNode: &castai.ActionPatchNode{
			NodeName:      nodeName,
			NodeID:        nodeID,
			ProviderId:    providerID,
			Labels:        labels,
			Annotations:   annotations,
			Taints:        taints,
			Capacity:      capacity,
			Unschedulable: unschedulable,
		},
	}
}
