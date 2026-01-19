package podmonitor

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
)

// Violation represents a constraint violation for a pod.
type Violation struct {
	Description string
	Permanent   bool
}

// Reason prefix for when node doesn't have enough resources to schedule the pod.
// See https://github.com/kubernetes/kubernetes/blob/master/pkg/kubelet/lifecycle/predicate.go
const insufficientResourcePrefix = "OutOf"

// CheckScheduled verifies that a pod has been scheduled.
// Returns a violation if the pod is not scheduled.
func CheckScheduled(pod *corev1.Pod) *Violation {
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodScheduled && cond.Status == corev1.ConditionTrue {
			return nil
		}
	}

	return &Violation{
		Description: "pod is not scheduled",
		// If the pod failed due to resource constraints, it's a permanent violation
		// as those resources won't free up soon in our rebalancing context
		Permanent: isFailedDueToResources(pod),
	}
}

// CheckCorrectNode verifies that a pod is running on its target node.
// Returns a violation if the pod is scheduled to the wrong node.
func CheckCorrectNode(pod *corev1.Pod, targetNode string) *Violation {
	// Pod not yet assigned to a node
	if pod.Spec.NodeName == "" {
		return nil
	}

	// Pod is on the wrong node
	if pod.Spec.NodeName != targetNode {
		return &Violation{
			Description: "pod is on wrong node: expected " + targetNode + ", got " + pod.Spec.NodeName,
			Permanent:   true, // Pod landed on wrong node, need to evict and reschedule
		}
	}

	return nil
}

// CheckNotDraining verifies that a node is not being drained.
// Returns a violation if the node has the draining label.
func CheckNotDraining(node *corev1.Node) *Violation {
	if node == nil {
		return nil
	}

	isDraining := len(node.Labels) > 0 && node.Labels[LabelNodeDraining] != ""
	if isDraining {
		return &Violation{
			Description: "node is draining",
			// Draining might fail and pod might be able to continue running,
			// but it's more likely to be the reason for the failure.
			// If the node was deemed to be drained, we should get the pod out of it.
			Permanent: true,
		}
	}

	return nil
}

// isFailedDueToResources checks if a pod failed because the node doesn't have
// enough resources (OutOfCPU, OutOfMemory, etc.)
func isFailedDueToResources(pod *corev1.Pod) bool {
	return pod.Status.Phase == corev1.PodFailed &&
		strings.HasPrefix(pod.Status.Reason, insufficientResourcePrefix)
}
