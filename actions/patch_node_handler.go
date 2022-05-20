package actions

import (
	"context"
	"errors"
	"fmt"

	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/castai/cluster-controller/castai"
)

func newPatchNodeHandler(log logrus.FieldLogger, clientset kubernetes.Interface) ActionHandler {
	return &patchNodeHandler{
		log:       log,
		clientset: clientset,
	}
}

type patchNodeHandler struct {
	log       logrus.FieldLogger
	clientset kubernetes.Interface
}

func (h *patchNodeHandler) Handle(ctx context.Context, data interface{}) error {
	req, ok := data.(*castai.ActionPatchNode)
	if !ok {
		return fmt.Errorf("unexpected type %T for delete patch handler", data)
	}
	for k := range req.Labels {
		if k == "" {
			return errors.New("labels contain entry with empty key")
		}
	}
	for k := range req.Annotations {
		if k == "" {
			return errors.New("annotations contain entry with empty key")
		}
	}
	for _, t := range req.Taints {
		if t.Key == "" {
			return errors.New("taint contain entry with empty key")
		}
	}

	log := h.log.WithField("node_name", req.NodeName)

	node, err := h.clientset.CoreV1().Nodes().Get(ctx, req.NodeName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.Info("node not found, skipping patch")
			return nil
		}
		return err
	}

	log.Infof("patching node, labels=%v, taints=%v, annotations=%v", req.Labels, req.Taints, req.Annotations)

	return patchNode(ctx, h.clientset, node, func(n *v1.Node) error {
		node.Labels = patchNodeMapField(node.Labels, req.Labels)
		node.Annotations = patchNodeMapField(node.Annotations, req.Annotations)
		node.Spec.Taints = patchTaints(node.Spec.Taints, req.Taints)
		return nil
	})
}

func patchNodeMapField(values map[string]string, patch map[string]string) map[string]string {
	if values == nil {
		values = map[string]string{}
	}

	for k, v := range patch {
		if k[0] == '-' {
			delete(values, k[1:])
		} else {
			values[k] = v
		}
	}
	return values
}

func patchTaints(taints []v1.Taint, patch []castai.NodeTaint) []v1.Taint {
	for _, v := range patch {
		taint := &v1.Taint{Key: v.Key, Value: v.Value, Effect: v1.TaintEffect(v.Effect)}
		if v.Key[0] == '-' {
			taint.Key = taint.Key[1:]
			taints = deleteTaint(taints, taint)
		} else if _, found := findTaint(taints, taint); !found {
			taints = append(taints, *taint)
		}
	}
	return taints
}

func findTaint(taints []v1.Taint, t *v1.Taint) (v1.Taint, bool) {
	for _, taint := range taints {
		if taint.MatchTaint(t) {
			return taint, true
		}
	}
	return v1.Taint{}, false
}

func deleteTaint(taints []v1.Taint, t *v1.Taint) []v1.Taint {
	var res []v1.Taint
	for _, taint := range taints {
		if !taint.MatchTaint(t) {
			res = append(res, taint)
		}
	}
	return res
}
