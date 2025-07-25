package actions

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/samber/lo"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"

	"github.com/castai/cluster-controller/internal/castai"
	"github.com/castai/cluster-controller/internal/waitext"
)

var _ ActionHandler = &DeleteNodeHandler{}

type deleteNodeConfig struct {
	deleteRetries       int
	deleteRetryWait     time.Duration
	podsTerminationWait time.Duration
}

func NewDeleteNodeHandler(log logrus.FieldLogger, clientset kubernetes.Interface) *DeleteNodeHandler {
	return &DeleteNodeHandler{
		log:       log,
		clientset: clientset,
		cfg: deleteNodeConfig{
			deleteRetries:       10,
			deleteRetryWait:     5 * time.Second,
			podsTerminationWait: 30 * time.Second,
		},
		DrainNodeHandler: DrainNodeHandler{
			log:       log,
			clientset: clientset,
		},
	}
}

type DeleteNodeHandler struct {
	DrainNodeHandler
	log       logrus.FieldLogger
	clientset kubernetes.Interface
	cfg       deleteNodeConfig
}

func (h *DeleteNodeHandler) Handle(ctx context.Context, action *castai.ClusterAction) error {
	if action == nil {
		return fmt.Errorf("action is nil %w", errAction)
	}
	req, ok := action.Data().(*castai.ActionDeleteNode)
	if !ok {
		return newUnexpectedTypeErr(action.Data(), req)
	}

	log := h.log.WithFields(logrus.Fields{
		"node_name":      req.NodeName,
		"node_id":        req.NodeID,
		"provider_id":    req.ProviderId,
		"type":           reflect.TypeOf(action.Data().(*castai.ActionDeleteNode)).String(),
		ActionIDLogField: action.ID,
	})

	log.Info("deleting kubernetes node")
	if req.NodeName == "" ||
		(req.NodeID == "" && req.ProviderId == "") {
		return fmt.Errorf("node name or node ID/provider ID is empty %w", errAction)
	}

	b := waitext.NewConstantBackoff(h.cfg.deleteRetryWait)
	err := waitext.Retry(
		ctx,
		b,
		h.cfg.deleteRetries,
		func(ctx context.Context) (bool, error) {
			current, err := getNodeByIDs(ctx, h.clientset.CoreV1().Nodes(), req.NodeName, req.NodeID, req.ProviderId, log)
			if err != nil {
				if errors.Is(err, errNodeNotFound) || errors.Is(err, errNodeDoesNotMatch) {
					return false, err
				}

				return true, err
			}

			err = h.clientset.CoreV1().Nodes().Delete(ctx, current.Name, metav1.DeleteOptions{
				Preconditions: &metav1.Preconditions{
					UID: &current.UID,
				},
			})
			if apierrors.IsNotFound(err) {
				return false, errNodeNotFound
			}
			return true, err
		},
		func(err error) {
			h.log.Warnf("error deleting kubernetes node, will retry: %v", err)
		},
	)

	if errors.Is(err, errNodeNotFound) || errors.Is(err, errNodeDoesNotMatch) {
		log.Infof("node already deleted or does not match the requested node ID/provider ID %v", err)
		return nil
	}

	if err != nil {
		log.Errorf("error deleting kubernetes node: %v", err)
		return fmt.Errorf("error removing node %w", err)
	}

	podsListingBackoff := waitext.NewConstantBackoff(h.cfg.podsTerminationWait)
	var pods []v1.Pod
	err = waitext.Retry(
		ctx,
		podsListingBackoff,
		h.cfg.deleteRetries,
		func(ctx context.Context) (bool, error) {
			podList, err := h.clientset.CoreV1().Pods(metav1.NamespaceAll).List(ctx, metav1.ListOptions{
				FieldSelector: fields.SelectorFromSet(fields.Set{"spec.nodeName": req.NodeName}).String(),
			})
			if err != nil {
				return true, err
			}
			pods = podList.Items
			return false, nil
		},
		func(err error) {
			h.log.Warnf("error listing pods, will retry: %v", err)
		},
	)
	if err != nil {
		return fmt.Errorf("listing node pods %w", err)
	}

	log.Infof("node has %d pods - removing", len(pods))

	// Create delete options with grace period 0 - force delete.
	deleteOptions := metav1.NewDeleteOptions(0)
	deletePod := func(ctx context.Context, pod v1.Pod) error {
		return h.deletePod(ctx, *deleteOptions, pod)
	}

	deletedPods, failedPods := executeBatchPodActions(ctx, log, pods, deletePod, "delete-pod")
	log.Infof("successfully deleted %d pods, failed to delete %d pods", len(deletedPods), len(failedPods))

	if err := h.deleteNodeVolumeAttachments(ctx, req.NodeName); err != nil {
		log.Warnf("deleting volume attachments: %v", err)
	}
	// Cleanup of pods for which node has been removed and remove volume attachments.
	// It should take a few seconds but added retry in case of network errors.
	podsWaitBackoff := waitext.NewConstantBackoff(h.cfg.podsTerminationWait)
	return waitext.Retry(
		ctx,
		podsWaitBackoff,
		h.cfg.deleteRetries,
		func(ctx context.Context) (bool, error) {
			pods, err := h.clientset.CoreV1().Pods(metav1.NamespaceAll).List(ctx, metav1.ListOptions{
				FieldSelector: fields.SelectorFromSet(fields.Set{"spec.nodeName": req.NodeName}).String(),
			})
			if err != nil {
				return true, fmt.Errorf("unable to list pods for node %q err: %w", req.NodeName, err)
			}

			runningPods := lo.Filter(pods.Items, func(p v1.Pod, _ int) bool {
				// We don't care about non-running pods, node is being deleted anyway.
				return p.Status.Phase == v1.PodRunning
			})
			if len(runningPods) > 0 {
				return true, fmt.Errorf("waiting for %d pods to be terminated on node %v", len(pods.Items), req.NodeName)
			}
			// Check if there are any volume attachments left for the node, but don't block on waiting for them.
			volumeAttachments, err := h.clientset.StorageV1().VolumeAttachments().List(ctx, metav1.ListOptions{})
			if err != nil && !apierrors.IsForbidden(err) {
				log.Warnf("unable to list volume attachments for node %q err: %v", req.NodeName, err)
			} else if volumeAttachments != nil && len(volumeAttachments.Items) > 0 {
				log.Infof("currently %d volume attachments to be deleted on node %v",
					len(volumeAttachments.Items), req.NodeName)
			}
			return false, nil
		},
		func(err error) {
			h.log.Warnf("error waiting for pods termination, will retry: %v", err)
		},
	)
}

func (h *DeleteNodeHandler) deleteNodeVolumeAttachments(ctx context.Context, nodeName string) error {
	volumeAttachments, err := h.clientset.StorageV1().VolumeAttachments().List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	// Force delete volume attachments for the node.
	gracePeriod := int64(0)
	for _, va := range volumeAttachments.Items {
		if va.Spec.NodeName == nodeName {
			// Delete the volume attachment.
			if err := h.clientset.StorageV1().VolumeAttachments().
				Delete(ctx, va.Name, metav1.DeleteOptions{
					GracePeriodSeconds: &gracePeriod,
				}); err != nil {
				return err
			}
		}
	}
	return nil
}
