package volume

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

type DetachmentWaitOptions struct {
	NodeName string
	// Timeout is the maximum time to wait for volumes to detach.
	// If zero, the waiter's default timeout is used.
	Timeout time.Duration

	// PodsToExclude are pods whose VolumeAttachments should not be waited for
	// (e.g., DaemonSet pods, static pods that won't be evicted).
	PodsToExclude []v1.Pod
}

// DetachmentWaiter waits for VolumeAttachments to be detached from a node.
type DetachmentWaiter interface {
	// Wait waits for all VolumeAttachments on the specified node to be deleted.
	// VolumeAttachments belonging to opts.PodsToExclude are not waited for.
	// If opts.Timeout is zero, the waiter's default timeout is used.
	// Returns nil on success.
	// Returns error on timeout, unexpected failures or context cancellation.
	Wait(ctx context.Context, log logrus.FieldLogger, opts DetachmentWaitOptions) error
}

type DetachmentError struct {
	RemainingVAs []string
}

func (e *DetachmentError) Error() string {
	return fmt.Sprintf("volume detachment timed out, remaining VolumeAttachments: %v", e.RemainingVAs)
}

type detachmentWaiter struct {
	clientset      kubernetes.Interface
	vaIndexer      cache.Indexer
	pollInterval   time.Duration
	defaultTimeout time.Duration
}

// NewDetachmentWaiter creates a new DetachmentWaiter.
// Returns nil if vaIndexer is nil.
// defaultTimeout is the timeout to use when DetachmentWaitOptions.Timeout is zero.
// If defaultTimeout is zero, DefaultVolumeDetachTimeout is used.
func NewDetachmentWaiter(
	clientset kubernetes.Interface,
	vaIndexer cache.Indexer,
	pollInterval time.Duration,
	defaultTimeout time.Duration,
) DetachmentWaiter {
	if vaIndexer == nil {
		return nil
	}
	if defaultTimeout == 0 {
		defaultTimeout = DefaultVolumeDetachTimeout
	}
	return &detachmentWaiter{
		clientset:      clientset,
		vaIndexer:      vaIndexer,
		pollInterval:   pollInterval,
		defaultTimeout: defaultTimeout,
	}
}

// Wait implements DetachmentWaiter.
func (w *detachmentWaiter) Wait(
	ctx context.Context,
	log logrus.FieldLogger,
	opts DetachmentWaitOptions,
) error {
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = w.defaultTimeout
	}

	excludedPVs := w.getExcludedPVsForNode(ctx, log, opts.PodsToExclude)

	vaCtx, vaCancel := context.WithTimeout(ctx, timeout)
	defer vaCancel()

	return w.waitForVolumeDetach(vaCtx, log, opts.NodeName, excludedPVs, timeout)
}

// getExcludedPVsForNode returns PV names that should NOT be waited for.
// These are PVs used by podsToExclude (e.g., DaemonSets, static pods) since
// those pods won't be evicted and would cause a deadlock waiting for their VAs.
func (w *detachmentWaiter) getExcludedPVsForNode(
	ctx context.Context,
	log logrus.FieldLogger,
	podsToExclude []v1.Pod,
) map[string]struct{} {
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
	return excludedPVs
}

// waitForVolumeDetach waits for all VolumeAttachments on the node to be deleted,
// except those belonging to excludedPVs (e.g., DaemonSet pods).
// Returns nil when all VAs are deleted.
// Returns DetachmentError on timeout with the list of remaining VAs.
// Respects context cancellation.
func (w *detachmentWaiter) waitForVolumeDetach(
	ctx context.Context,
	log logrus.FieldLogger,
	nodeName string,
	excludedPVs map[string]struct{},
	timeout time.Duration,
) error {
	// Get initial count for logging
	initialVAs, _ := w.vaIndexer.ByIndex(informer.VANodeNameIndexer, nodeName)
	log.Infof("waiting for VolumeAttachments to detach with timeout %v (found %d on node, excluding %d)",
		timeout, len(initialVAs), len(excludedPVs))

	var lastRemainingNames []string

	err := waitext.Retry(
		ctx,
		waitext.NewConstantBackoff(w.pollInterval),
		waitext.Forever,
		func(ctx context.Context) (bool, error) {
			vaObjects, err := w.vaIndexer.ByIndex(informer.VANodeNameIndexer, nodeName)
			if err != nil {
				return true, fmt.Errorf("listing VolumeAttachments: %w", err)
			}

			lastRemainingNames = nil
			for _, obj := range vaObjects {
				va, ok := obj.(*storagev1.VolumeAttachment)
				if !ok {
					continue
				}
				if va.Spec.Source.PersistentVolumeName == nil {
					continue
				}
				if _, excluded := excludedPVs[*va.Spec.Source.PersistentVolumeName]; excluded {
					continue
				}
				lastRemainingNames = append(lastRemainingNames, va.Name)
			}

			if len(lastRemainingNames) == 0 {
				log.Info("all VolumeAttachments have been detached")
				return false, nil
			}

			return true, fmt.Errorf("waiting for %d VolumeAttachments to detach: %v", len(lastRemainingNames), lastRemainingNames)
		},
		func(err error) {
			log.Debugf("waiting for volume detach: %v", err)
		},
	)

	if errors.Is(err, context.DeadlineExceeded) {
		return &DetachmentError{
			RemainingVAs: lastRemainingNames,
		}
	}

	return err
}
