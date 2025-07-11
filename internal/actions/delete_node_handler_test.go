package actions

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/castai/cluster-controller/internal/castai"
)

//nolint:goconst
func TestDeleteNodeHandler(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)

	t.Run("delete successfully", func(t *testing.T) {
		r := require.New(t)
		node := &v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: nodeName,
				Labels: map[string]string{
					castai.LabelNodeID: "node-id",
				},
			},
		}
		clientset := fake.NewSimpleClientset(node)

		action := &castai.ClusterAction{
			ID: uuid.New().String(),
			ActionDeleteNode: &castai.ActionDeleteNode{
				NodeName: "node1",
				NodeID:   "node-id",
			},
		}

		h := DeleteNodeHandler{
			log:       log,
			clientset: clientset,
			cfg:       deleteNodeConfig{},
		}

		err := h.Handle(context.Background(), action)
		r.NoError(err)

		_, err = clientset.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
		r.Error(err)
		r.True(apierrors.IsNotFound(err))
	})

	t.Run("skip delete when node not found", func(t *testing.T) {
		r := require.New(t)
		node := &v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: nodeName,
			},
		}
		clientset := fake.NewSimpleClientset(node)

		action := &castai.ClusterAction{
			ID: uuid.New().String(),
			ActionDeleteNode: &castai.ActionDeleteNode{
				NodeName: "already-deleted-node",
				NodeID:   "node-id",
			},
		}

		h := DeleteNodeHandler{
			log:       log,
			clientset: clientset,
			cfg:       deleteNodeConfig{},
		}

		err := h.Handle(context.Background(), action)
		r.NoError(err)

		_, err = clientset.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
		r.NoError(err)
	})

	t.Run("skip delete when node id do not match", func(t *testing.T) {
		r := require.New(t)
		node := &v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: nodeName,
				Labels: map[string]string{
					castai.LabelNodeID: "node-id",
				},
			},
		}
		clientset := fake.NewSimpleClientset(node)

		action := &castai.ClusterAction{
			ID: uuid.New().String(),
			ActionDeleteNode: &castai.ActionDeleteNode{
				NodeName: "node1",
				NodeID:   "another-node-id",
			},
		}

		h := DeleteNodeHandler{
			log:       log,
			clientset: clientset,
			cfg:       deleteNodeConfig{},
		}

		err := h.Handle(context.Background(), action)
		r.NoError(err)

		existing, err := clientset.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
		r.NoError(err)
		existing.Labels[castai.LabelNodeID] = "node-id"
	})

	t.Run("delete node with pods", func(t *testing.T) {
		r := require.New(t)
		clientset := setupFakeClientWithNodePodEviction(nodeName, nodeID, providerID, podName)

		action := &castai.ClusterAction{
			ID: uuid.New().String(),
			ActionDeleteNode: &castai.ActionDeleteNode{
				NodeName: nodeName,
				NodeID:   "node-id",
			},
		}

		h := DeleteNodeHandler{
			log:       log,
			clientset: clientset,
			cfg: deleteNodeConfig{
				podsTerminationWait: 1,
			},
			DrainNodeHandler: DrainNodeHandler{clientset: clientset, log: log},
		}

		err := h.Handle(context.Background(), action)
		r.NoError(err)

		_, err = clientset.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
		r.Error(err)
		r.True(apierrors.IsNotFound(err))

		pods, err := h.clientset.CoreV1().Pods(metav1.NamespaceAll).List(context.Background(), metav1.ListOptions{
			FieldSelector: fields.SelectorFromSet(fields.Set{"spec.nodeName": nodeName}).String(),
		})
		r.NoError(err)
		r.Len(pods.Items, 0)
		va, err := h.clientset.StorageV1().VolumeAttachments().List(context.Background(), metav1.ListOptions{
			FieldSelector: fields.SelectorFromSet(fields.Set{}).String(),
		})
		r.NoError(err)
		r.Len(va.Items, 0)
	})
}

func TestDeleteNodeHandler_Handle(t *testing.T) {
	t.Parallel()

	type fields struct {
		DrainNodeHandler DrainNodeHandler
		clientSet        func() *fake.Clientset
		cfg              deleteNodeConfig
	}
	type args struct {
		action *castai.ClusterAction
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr error
	}{
		{
			name: "nil",
			args: args{},
			fields: fields{
				clientSet: func() *fake.Clientset {
					return fake.NewClientset()
				},
			},
			wantErr: errAction,
		},
		{
			name: "wrong action type",
			args: args{
				action: &castai.ClusterAction{
					ActionDrainNode: &castai.ActionDrainNode{},
				},
			},
			fields: fields{
				clientSet: func() *fake.Clientset {
					return fake.NewClientset()
				},
			},
			wantErr: errAction,
		},
		{
			name: "empty node name",
			args: args{
				action: newActionDeleteNode("", nodeID, providerID),
			},
			fields: fields{
				clientSet: func() *fake.Clientset {
					return setupFakeClientWithNodePodEviction(nodeName, nodeID, providerID, podName)
				},
			},
			wantErr: errAction,
		},
		{
			name: "empty node ID and provider ID",
			args: args{
				action: newActionDeleteNode(nodeName, "", ""),
			},
			fields: fields{
				clientSet: func() *fake.Clientset {
					return setupFakeClientWithNodePodEviction(nodeName, nodeID, providerID, podName)
				},
			},
			wantErr: errAction,
		},
		{
			name: "action with another node id and provider id - node not found",
			fields: fields{
				clientSet: func() *fake.Clientset {
					return setupFakeClientWithNodePodEviction(nodeName, nodeID, providerID, podName)
				},
			},
			args: args{
				action: newActionDeleteNode(nodeName, "another-node-id", "another-provider-id"),
			},
			wantErr: errNodeDoesNotMatch,
		},
		{
			name: "action with proper node id and another provider id - node not found",
			fields: fields{
				clientSet: func() *fake.Clientset {
					return setupFakeClientWithNodePodEviction(nodeName, nodeID, providerID, podName)
				},
			},
			args: args{
				action: newActionDrainNode(nodeName, nodeID, "another-provider-id", 1, true),
			},
			wantErr: errNodeDoesNotMatch,
		},
		{
			name: "action with another node id and proper provider id - node not found",
			fields: fields{
				clientSet: func() *fake.Clientset {
					return setupFakeClientWithNodePodEviction(nodeName, nodeID, providerID, podName)
				},
			},
			args: args{
				action: newActionDrainNode(nodeName, nodeID, "another-provider-id", 1, true),
			},
			wantErr: errNodeDoesNotMatch,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := &DeleteNodeHandler{
				DrainNodeHandler: tt.fields.DrainNodeHandler,
				log:              logrus.New(),
				clientset:        tt.fields.clientSet(),
				cfg:              tt.fields.cfg,
			}
			err := h.Handle(context.Background(), tt.args.action)
			require.Equal(t, tt.wantErr != nil, err != nil, "expected error: %v, got: %v", tt.wantErr, err)
			if tt.wantErr != nil {
				require.ErrorAs(t, err, &tt.wantErr)
			}

			if err != nil {
				return
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
