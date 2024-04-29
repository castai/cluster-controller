package actions

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/castai/cluster-controller/types"
)

func TestCheckNodeDeletedHandler(t *testing.T) {
	r := require.New(t)

	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)

	t.Run("return error when node is not deleted", func(_ *testing.T) {
		nodeName := "node1"
		node := &v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: nodeName,
			},
		}
		clientset := fake.NewSimpleClientset(node)

		h := checkNodeDeletedHandler{
			log:       log,
			clientset: clientset,
			cfg:       checkNodeDeletedConfig{},
		}

		action := &types.ClusterAction{
			ID:                     uuid.New().String(),
			ActionCheckNodeDeleted: &types.ActionCheckNodeDeleted{NodeName: "node1"},
		}

		err := h.Handle(context.Background(), action)
		r.EqualError(err, "node is not deleted")
	})

	t.Run("handle check successfully when node is not found", func(_ *testing.T) {
		clientset := fake.NewSimpleClientset()

		h := checkNodeDeletedHandler{
			log:       log,
			clientset: clientset,
			cfg:       checkNodeDeletedConfig{},
		}

		action := &types.ClusterAction{
			ID:                     uuid.New().String(),
			ActionCheckNodeDeleted: &types.ActionCheckNodeDeleted{NodeName: "node1"},
		}

		err := h.Handle(context.Background(), action)
		r.NoError(err)
	})
}
