package actions

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/castai/cluster-controller/internal/informer"
)

// NewCheckNodeStatusHandler creates a handler for checking node status.
// If informerManager is provided, it uses efficient informer-based watching.
// If informerManager is nil, it falls back to polling-based implementation.
func NewCheckNodeStatusHandler(log logrus.FieldLogger, clientset kubernetes.Interface, informerManager *informer.Manager) ActionHandler {
	if informerManager != nil {
		log.Info("using informer-based check node status handler")
		return &checkNodeStatusInformerHandler{
			log:             log,
			clientset:       clientset,
			informerManager: informerManager,
		}
	}
	log.Info("using polling-based check node status handler")
	return &checkNodeStatusPollingHandler{
		log:       log,
		clientset: clientset,
	}
}

// Shared errors
var errNodeWatcherClosed = errors.New("node watcher closed")

// checkNodeDeleted checks if a node is deleted by verifying it doesn't exist or has been replaced.
// Returns (retry, error) where retry indicates if the operation should be retried.
func checkNodeDeleted(ctx context.Context, clientset kubernetes.Interface, nodeName, nodeID, providerID string, log logrus.FieldLogger) (bool, error) {
	n, err := getNodeByIDs(ctx, clientset.CoreV1().Nodes(), nodeName, nodeID, providerID, log)
	if isNodeDeletedOrReplaced(err) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	if n == nil {
		return false, nil
	}
	return false, errNodeNotDeleted
}

// isNodeDeletedOrReplaced checks if the error indicates the node was deleted or replaced.
func isNodeDeletedOrReplaced(err error) bool {
	return errors.Is(err, errNodeDoesNotMatch) || errors.Is(err, errNodeNotFound)
}

// containsUninitializedNodeTaint checks if taints contain the cloud provider uninitialized taint.
func containsUninitializedNodeTaint(taints []corev1.Taint) bool {
	for _, taint := range taints {
		if taint == taintCloudProviderUninitialized {
			return true
		}
	}
	return false
}

var taintCloudProviderUninitialized = corev1.Taint{
	Key:    "node.cloudprovider.kubernetes.io/uninitialized",
	Effect: corev1.TaintEffectNoSchedule,
}

// patchNodeCapacityIfNeeded patches the node capacity with network bandwidth if the label exists.
func patchNodeCapacityIfNeeded(ctx context.Context, log *logrus.Entry, clientset kubernetes.Interface, node *corev1.Node) {
	bandwidth, ok := node.Labels["scheduling.cast.ai/network-bandwidth"]
	if !ok {
		return
	}

	patch, err := json.Marshal(map[string]interface{}{
		"status": map[string]interface{}{
			"capacity": map[string]interface{}{
				"scheduling.cast.ai/network-bandwidth": bandwidth,
			},
		},
	})
	if err != nil {
		log.WithError(err).Error("failed to marshal node capacity patch")
		return
	}

	log.Infof("going to patch node capacity: %v", node.Name)
	if err := patchNodeStatus(ctx, log, clientset, node.Name, patch); err != nil {
		log.WithError(err).Error("failed to patch node capacity")
		return
	}
	log.Infof("patched node capacity: %v", node.Name)
}
