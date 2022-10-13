package actions

import (
	"context"
	"github.com/google/uuid"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/castai/cluster-controller/castai"
)

func TestDeleteNodeHandler(t *testing.T) {
	r := require.New(t)

	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)

	t.Run("delete successfully", func(t *testing.T) {
		nodeName := "node1"
		node := &v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: nodeName,
			},
		}
		clientset := fake.NewSimpleClientset(node)

		actionID, _ := uuid.NewUUID()
		h := deleteNodeHandler{
			log:       log,
			clientset: clientset,
			cfg:       deleteNodeConfig{},
		}

		req := &castai.ActionDeleteNode{
			NodeName: "node1",
		}

		err := h.Handle(context.Background(), req, actionID.String())
		r.NoError(err)

		_, err = clientset.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
		r.Error(err)
		r.True(apierrors.IsNotFound(err))
	})

	t.Run("skip delete when node not found", func(t *testing.T) {
		nodeName := "node1"
		node := &v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: nodeName,
			},
		}
		clientset := fake.NewSimpleClientset(node)

		actionID := uuid.New().String()
		h := deleteNodeHandler{
			log:       log,
			clientset: clientset,
			cfg:       deleteNodeConfig{},
		}

		req := &castai.ActionDeleteNode{
			NodeName: "already-deleted-node",
		}
		err := h.Handle(context.Background(), req, actionID)
		r.NoError(err)

		_, err = clientset.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
		r.NoError(err)
	})
}
