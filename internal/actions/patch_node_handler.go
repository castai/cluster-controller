package actions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"

	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"

	"github.com/castai/cluster-controller/internal/castai"
)

var _ ActionHandler = &PatchNodeHandler{}

func NewPatchNodeHandler(log logrus.FieldLogger, clientset kubernetes.Interface) *PatchNodeHandler {
	return &PatchNodeHandler{
		log:       log,
		clientset: clientset,
	}
}

type PatchNodeHandler struct {
	log       logrus.FieldLogger
	clientset kubernetes.Interface
}

func (h *PatchNodeHandler) Handle(ctx context.Context, action *castai.ClusterAction) error {
	req, ok := action.Data().(*castai.ActionPatchNode)
	if !ok {
		return fmt.Errorf("unexpected type %T for delete patch handler", action.Data())
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

	log := h.log.WithFields(logrus.Fields{
		"node_name":      req.NodeName,
		"node_id":        req.NodeID,
		"action":         reflect.TypeOf(action.Data().(*castai.ActionPatchNode)).String(),
		ActionIDLogField: action.ID,
	})

	node, err := getNodeForPatching(ctx, h.log, h.clientset, req.NodeName)
	if err != nil {
		if apierrors.IsNotFound(err) {
			log.WithError(err).Infof("node not found, skipping patch")
			return nil
		}
		return err
	}

	unschedulable := "<nil>"
	if req.Unschedulable != nil {
		unschedulable = strconv.FormatBool(*req.Unschedulable)
	}

	if req.Unschedulable == nil && len(req.Labels) == 0 && len(req.Taints) == 0 && len(req.Annotations) == 0 {
		log.Info("no patch for node spec or labels")
	} else {
		log.WithFields(map[string]interface{}{
			"labels":      req.Labels,
			"taints":      req.Taints,
			"annotations": req.Annotations,
			"capacity":    req.Capacity,
		}).Infof("patching node, labels=%v, taints=%v, annotations=%v, unschedulable=%v", req.Labels, req.Taints, req.Annotations, unschedulable)

		err = patchNode(ctx, h.log, h.clientset, node, func(n *v1.Node) {
			n.Labels = patchNodeMapField(n.Labels, req.Labels)
			n.Annotations = patchNodeMapField(n.Annotations, req.Annotations)
			n.Spec.Taints = patchTaints(n.Spec.Taints, req.Taints)
			n.Spec.Unschedulable = patchUnschedulable(n.Spec.Unschedulable, req.Unschedulable)
		})
		if err != nil {
			return err
		}
	}

	if len(req.Capacity) > 0 {
		log.WithField("capacity", req.Capacity).Infof("patching node status")
		patch, err := json.Marshal(map[string]interface{}{
			"status": map[string]interface{}{
				"capacity": req.Capacity,
			},
		})
		if err != nil {
			return fmt.Errorf("marshal patch for status: %w", err)
		}
		return patchNodeStatus(ctx, h.log, h.clientset, node.Name, patch)
	}
	return nil
}

func patchNodeMapField(values, patch map[string]string) map[string]string {
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

func patchUnschedulable(unschedulable bool, patch *bool) bool {
	if patch != nil {
		return *patch
	}
	return unschedulable
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
