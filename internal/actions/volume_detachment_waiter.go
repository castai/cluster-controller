package actions

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	"github.com/castai/cluster-controller/internal/informer"
	"github.com/castai/cluster-controller/internal/waitext"
)

const (
	DefaultVolumeDetachTimeout = 1 * time.Minute
)

// VolumeDetachmentWaitOptions configures the behavior of VolumeDetachmentWaiter.Wait().
type VolumeDetachmentWaitOptions struct {
	// NodeName is the name of the node to wait for volume detachments on. Required.
	NodeName string

	// Timeout is the maximum time to wait for volumes to detach.
	// If zero, defaults to DefaultVolumeDetachTimeout (1 minute).
	Timeout time.Duration

	// PodsToExclude are pods whose VolumeAttachments should not be waited for
	// (e.g., DaemonSet pods, static pods that won't be evicted).
	PodsToExclude []v1.Pod
}

// VolumeDetachmentWaiter waits for VolumeAttachments to be detached from a node.
type VolumeDetachmentWaiter interface {
	// Wait waits for all VolumeAttachments on the specified node to be deleted.
	// VolumeAttachments belonging to opts.PodsToExclude are not waited for.
	// If opts.Timeout is zero, defaults to DefaultVolumeDetachTimeout.
	// Returns nil on success or timeout (timeout is logged but not treated as error).
	// Returns error only for unexpected failures or context cancellation.
	Wait(ctx context.Context, log logrus.FieldLogger, opts VolumeDetachmentWaitOptions) error
}

type volumeDetachmentWaiter struct {
	clientset    kubernetes.Interface
	vaIndexer    cache.Indexer
	pollInterval time.Duration
}

// NewVolumeDetachmentWaiter creates a new VolumeDetachmentWaiter.
// Returns nil if vaIndexer is nil.
func NewVolumeDetachmentWaiter(
	clientset kubernetes.Interface,
	vaIndexer cache.Indexer,
	pollInterval time.Duration,
) VolumeDetachmentWaiter {
	if vaIndexer == nil {
		return nil
	}
	return &volumeDetachmentWaiter{
		clientset:    clientset,
		vaIndexer:    vaIndexer,
		pollInterval: pollInterval,
	}
}

// Wait implements VolumeDetachmentWaiter.
func (w *volumeDetachmentWaiter) Wait(
	ctx context.Context,
	log logrus.FieldLogger,
	opts VolumeDetachmentWaitOptions,
) error {
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = DefaultVolumeDetachTimeout
	}

	vaNames, err := w.getVolumeAttachmentsForNode(ctx, log, opts.NodeName, opts.PodsToExclude)
	if err != nil {
		log.Warnf("failed to get VolumeAttachments for node: %v", err)
		return nil
	}

	if len(vaNames) == 0 {
		log.Debug("no VolumeAttachments to wait for")
		return nil
	}

	vaCtx, vaCancel := context.WithTimeout(ctx, timeout)
	defer vaCancel()

	log.Infof("waiting for %d VolumeAttachments to detach with timeout %v", len(vaNames), timeout)
	return w.waitForVolumeDetach(vaCtx, log, opts.NodeName, vaNames)
}

// getVolumeAttachmentsForNode returns VolumeAttachment names that should be waited for.
// It uses the informer indexer to efficiently query VolumeAttachments by node name.
// VAs belonging to podsToExclude (e.g., DaemonSets, static pods) are excluded since
// those pods won't be evicted and would cause a deadlock waiting for their VAs.
func (w *volumeDetachmentWaiter) getVolumeAttachmentsForNode(
	ctx context.Context,
	log logrus.FieldLogger,
	nodeName string,
	podsToExclude []v1.Pod,
) ([]string, error) {
	vaObjects, err := w.vaIndexer.ByIndex(informer.VANodeNameIndexer, nodeName)
	if err != nil {
		return nil, fmt.Errorf("listing VolumeAttachments by index: %w", err)
	}

	if len(vaObjects) == 0 {
		log.Debug("no VolumeAttachments found for node")
		return nil, nil
	}

	log.Debugf("found %d VolumeAttachments for node %s", len(vaObjects), nodeName)

	excludedPVs := make(map[string]struct{})
	for i := range podsToExclude {
		pod := &podsToExclude[i]
		for _, vol := range pod.Spec.Volumes {
			if vol.PersistentVolumeClaim == nil {
				continue
			}
			// Direct API call for PVC lookup (rare case - DaemonSets with PVCs)
			pvc, err := w.clientset.CoreV1().PersistentVolumeClaims(pod.Namespace).Get(
				ctx, vol.PersistentVolumeClaim.ClaimName, metav1.GetOptions{})
			if err == nil && pvc.Spec.VolumeName != "" {
				excludedPVs[pvc.Spec.VolumeName] = struct{}{}
				log.Debugf("excluding PV %s used by excluded pod %s/%s", pvc.Spec.VolumeName, pod.Namespace, pod.Name)
			}
		}
	}

	var vaNames []string
	for _, obj := range vaObjects {
		va, ok := obj.(*storagev1.VolumeAttachment)
		if !ok {
			continue
		}
		if va.Spec.Source.PersistentVolumeName == nil {
			continue
		}
		if _, excluded := excludedPVs[*va.Spec.Source.PersistentVolumeName]; !excluded {
			vaNames = append(vaNames, va.Name)
		}
	}

	log.Debugf("found %d VolumeAttachments to wait for on node %s (excluded %d from non-drainable pods)",
		len(vaNames), nodeName, len(excludedPVs))
	return vaNames, nil
}

// waitForVolumeDetach waits for the specified VolumeAttachments to be deleted.
// Returns nil when all VAs are deleted or when timeout is reached (logs warning with remaining VAs).
// Respects context cancellation.
func (w *volumeDetachmentWaiter) waitForVolumeDetach(
	ctx context.Context,
	log logrus.FieldLogger,
	nodeName string,
	vaNames []string,
) error {
	if len(vaNames) == 0 {
		return nil
	}

	vaNameSet := make(map[string]struct{}, len(vaNames))
	for _, name := range vaNames {
		vaNameSet[name] = struct{}{}
	}

	log.Infof("waiting for %d VolumeAttachments to detach: %v", len(vaNames), vaNames)

	var lastRemainingNames []string

	err := waitext.Retry(
		ctx,
		waitext.NewConstantBackoff(w.pollInterval),
		waitext.Forever,
		func(ctx context.Context) (bool, error) {
			// List VolumeAttachments for this node using indexer
			vaObjects, err := w.vaIndexer.ByIndex(informer.VANodeNameIndexer, nodeName)
			if err != nil {
				return true, fmt.Errorf("listing VolumeAttachments: %w", err)
			}

			// Check which of our VAs still exist
			remaining := 0
			lastRemainingNames = nil
			for _, obj := range vaObjects {
				va, ok := obj.(*storagev1.VolumeAttachment)
				if !ok {
					continue
				}
				if _, ok := vaNameSet[va.Name]; ok {
					remaining++
					lastRemainingNames = append(lastRemainingNames, va.Name)
				}
			}

			if remaining == 0 {
				log.Info("all VolumeAttachments have been detached")
				return false, nil
			}

			return true, fmt.Errorf("waiting for %d VolumeAttachments to detach: %v", remaining, lastRemainingNames)
		},
		func(err error) {
			log.Debugf("waiting for volume detach: %v", err)
		},
	)

	// Handle timeout gracefully - log warning with remaining VAs but don't fail
	if errors.Is(err, context.DeadlineExceeded) {
		log.Warnf("timeout waiting for VolumeAttachments to detach, remaining: %v, proceeding anyway", lastRemainingNames)
		return nil
	}

	return err
}
