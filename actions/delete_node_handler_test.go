package actions

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"k8s.io/apimachinery/pkg/fields"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/castai/cluster-controller/castai"
)

func TestDeleteNodeHandler(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)

	t.Run("delete successfully", func(t *testing.T) {
		r := require.New(t)
		nodeName := "node1"
		node := &v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: nodeName,
			},
		}
		clientset := fake.NewSimpleClientset(node)

		action := &castai.ClusterAction{
			ID: uuid.New().String(),
			ActionDeleteNode: &castai.ActionDeleteNode{
				NodeName: "node1",
			},
		}

		h := deleteNodeHandler{
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
		nodeName := "node1"
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
			},
		}

		h := deleteNodeHandler{
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
		nodeName := "node1"
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

		h := deleteNodeHandler{
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
		nodeName := "node1"
		podName := "pod1"
		clientset := setupFakeClientWithNodePodEviction(nodeName, podName)

		action := &castai.ClusterAction{
			ID: uuid.New().String(),
			ActionDeleteNode: &castai.ActionDeleteNode{
				NodeName: nodeName,
			},
		}

		h := deleteNodeHandler{
			log:       log,
			clientset: clientset,
			cfg: deleteNodeConfig{
				podsTerminationWait: 1,
			},
			drainNodeHandler: drainNodeHandler{clientset: clientset},
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
			FieldSelector: fields.SelectorFromSet(fields.Set{"spec.nodeName": nodeName}).String(),
		})
		r.NoError(err)
		r.Len(va.Items, 0)
	})
}
