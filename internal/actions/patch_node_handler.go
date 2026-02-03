package actions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"time"

	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/castai/cluster-controller/internal/castai"
	"github.com/castai/cluster-controller/internal/k8s"
	"github.com/castai/cluster-controller/internal/waitext"
)

var _ ActionHandler = &PatchNodeHandler{}

func NewPatchNodeHandler(log logrus.FieldLogger, clientset kubernetes.Interface) *PatchNodeHandler {
	return &PatchNodeHandler{
		retryTimeout: 5 * time.Second, // default timeout for retrying node patching
		log:          log,
		clientset:    clientset,
	}
}

type PatchNodeHandler struct {
	retryTimeout time.Duration
	log          logrus.FieldLogger
	clientset    kubernetes.Interface
}

func (h *PatchNodeHandler) Handle(ctx context.Context, action *castai.ClusterAction) error {
	if action == nil || action.Data() == nil {
		return fmt.Errorf("action or action data is nil %w", k8s.ErrAction)
	}
	req, ok := action.Data().(*castai.ActionPatchNode)
	if !ok {
		return newUnexpectedTypeErr(action.Data(), req)
	}
	for k := range req.Labels {
		if k == "" {
			return fmt.Errorf("labels contain entry with empty key %w", k8s.ErrAction)
		}
	}
	for k := range req.Annotations {
		if k == "" {
			return fmt.Errorf("annotations contain entry with empty key %w", k8s.ErrAction)
		}
	}
	for _, t := range req.Taints {
		if t.Key == "" {
			return fmt.Errorf("taints contain entry with empty key %w", k8s.ErrAction)
		}
	}

	log := h.log.WithFields(logrus.Fields{
		"node_name":      req.NodeName,
		"node_id":        req.NodeID,
		"provider_id":    req.ProviderId,
		"action":         reflect.TypeOf(action.Data().(*castai.ActionPatchNode)).String(),
		ActionIDLogField: action.ID,
	})

	log.Info("patching kubernetes node")
	if req.NodeName == "" ||
		(req.NodeID == "" && req.ProviderId == "") {
		return fmt.Errorf("node name or node ID/provider ID is empty %w", k8s.ErrAction)
	}

	node, err := h.getNodeForPatching(ctx, req.NodeName, req.NodeID, req.ProviderId)
	if err != nil {
		if errors.Is(err, k8s.ErrNodeNotFound) {
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
		log.WithFields(map[string]any{
			"labels":      req.Labels,
			"taints":      req.Taints,
			"annotations": req.Annotations,
			"capacity":    req.Capacity,
		}).Infof("patching node, labels=%v, taints=%v, annotations=%v, unschedulable=%v", req.Labels, req.Taints, req.Annotations, unschedulable)

		err = k8s.PatchNode(ctx, h.log, h.clientset, node, func(n *v1.Node) {
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
		patch, err := json.Marshal(map[string]any{
			"status": map[string]any{
				"capacity": req.Capacity,
			},
		})
		if err != nil {
			return fmt.Errorf("marshal patch for status: %w", err)
		}
		return k8s.PatchNodeStatus(ctx, h.log, h.clientset, node.Name, patch)
	}
	return nil
}

func (h *PatchNodeHandler) getNodeForPatching(ctx context.Context, nodeName, nodeID, providerID string) (*v1.Node, error) {
	// on GKE we noticed that sometimes the node is not found, even though it is in the cluster
	// as a result was returned from watch. But subsequent get request returns not found.
	// This is likely due to clientset's caching that's meant to alleviate API's load.
	// So we give enough time for cache to sync - ~10s max.

	var node *v1.Node

	boff := waitext.DefaultExponentialBackoff()
	boff.Duration = h.retryTimeout

	err := waitext.Retry(
		ctx,
		boff,
		5,
		func(ctx context.Context) (bool, error) {
			var err error
			node, err = k8s.GetNodeByIDs(ctx, h.clientset.CoreV1().Nodes(), nodeName, nodeID, providerID, h.log)
			if err != nil {
				return true, err
			}
			return false, nil
		},
		func(err error) {
			h.log.Warnf("getting node, will retry: %v", err)
		},
	)
	if err != nil {
		return nil, err
	}
	return node, nil
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
