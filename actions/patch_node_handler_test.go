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
				Labels: map[string]string{
					"l1": "v1",
				},
				Annotations: map[string]string{
					"a1": "v1",
				},
			},
			Spec: v1.NodeSpec{
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
		}
		clientset := fake.NewSimpleClientset(node)

		h := patchNodeHandler{
			log:       log,
			clientset: clientset,
		}

		req := &castai.ActionPatchNode{
			NodeName: "node1",
			Labels: map[string]string{
				"-l1": "",
				"l2":  "v2",
			},
			Annotations: map[string]string{
				"-a1": "",
				"a2":  "",
			},
			Taints: []castai.NodeTaint{
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
		}

		err := h.Handle(context.Background(), req)
		r.NoError(err)

		n, err := clientset.CoreV1().Nodes().Get(context.Background(), nodeName, metav1.GetOptions{})
		r.NoError(err)

		expectedLabels := map[string]string{
			"l2": "v2",
		}
		r.Equal(expectedLabels, n.Labels)

		expectedAnnotations := map[string]string{
			"a2": "",
		}
		r.Equal(expectedAnnotations, n.Annotations)

		expectedTaints := []v1.Taint{
			{Key: "t1", Value: "v1", Effect: "NoSchedule", TimeAdded: (*metav1.Time)(nil)},
			{Key: "t3", Value: "t3", Effect: "NoSchedule", TimeAdded: (*metav1.Time)(nil)},
		}
		r.Equal(expectedTaints, n.Spec.Taints)
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
