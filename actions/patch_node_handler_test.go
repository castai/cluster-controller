package actions

import (
	"context"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/castai/cluster-controller/castai"
)

func TestPatchNodeHandler(t *testing.T) {
	r := require.New(t)

	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)

	t.Run("patch successfully", func(t *testing.T) {
		nodeName := "node1"
		node := &v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: nodeName,
			},
		}
		clientset := fake.NewSimpleClientset(node)

		h := patchNodeHandler{
			log:       log,
			clientset: clientset,
		}

		req := &castai.ActionPatchNode{
			NodeName: "node1",
			Labels: map[string]string{
				"label": "ok",
			},
			Taints: []castai.NodeTaint{
				{
					Key:    "taint",
					Value:  "ok2",
					Effect: "NoSchedule",
				},
			},
		}

		err := h.Handle(context.Background(), req)
		r.NoError(err)

		n, err := clientset.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
		r.NoError(err)
		r.Equal("ok", n.Labels["label"])
		r.Equal([]v1.Taint{{Key: "taint", Value: "ok2", Effect: "NoSchedule", TimeAdded: (*metav1.Time)(nil)}}, n.Spec.Taints)
	})

	t.Run("skip patch when node not found", func(t *testing.T) {
		nodeName := "node1"
		node := &v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: nodeName,
			},
		}
		clientset := fake.NewSimpleClientset(node)

		h := patchNodeHandler{
			log:       log,
			clientset: clientset,
		}

		req := &castai.ActionPatchNode{
			NodeName: "already-deleted-node",
		}

		err := h.Handle(context.Background(), req)
		r.NoError(err)

		_, err = clientset.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
		r.NoError(err)
	})
}
