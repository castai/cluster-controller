package actions

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/kubernetes"

	"github.com/castai/cluster-controller/castai"
)

type deleteNodeConfig struct {
	deleteRetries       uint64
	deleteRetryWait     time.Duration
	podsTerminationWait time.Duration
}

var errNodeMismatch = errors.New("node id mismatch")

func newDeleteNodeHandler(log logrus.FieldLogger, clientset kubernetes.Interface) ActionHandler {
	return &deleteNodeHandler{
		log:       log,
		clientset: clientset,
		cfg: deleteNodeConfig{
			deleteRetries:       10,
			deleteRetryWait:     5 * time.Second,
			podsTerminationWait: 30 * time.Second,
		},
		drainNodeHandler: drainNodeHandler{
			log:       log,
			clientset: clientset,
		},
	}
}

type deleteNodeHandler struct {
	drainNodeHandler
	log       logrus.FieldLogger
	clientset kubernetes.Interface
	cfg       deleteNodeConfig
}

func (h *deleteNodeHandler) Handle(ctx context.Context, action *castai.ClusterAction) error {
	req, ok := action.Data().(*castai.ActionDeleteNode)
	if !ok {
		return fmt.Errorf("unexpected type %T for delete node handler", action.Data())
	}

	log := h.log.WithFields(logrus.Fields{
		"node_name":      req.NodeName,
		"node_id":        req.NodeID,
		"type":           reflect.TypeOf(action.Data().(*castai.ActionDeleteNode)).String(),
		actionIDLogField: action.ID,
	})
	log.Info("deleting kubernetes node")

	b := backoff.WithContext(backoff.WithMaxRetries(backoff.NewConstantBackOff(h.cfg.deleteRetryWait), h.cfg.deleteRetries), ctx)
	err := backoff.Retry(func() error {
		current, err := h.clientset.CoreV1().Nodes().Get(ctx, req.NodeName, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				log.Info("node not found, skipping delete")
				return nil
			}
			return fmt.Errorf("error getting node: %w", err)
		}

		if val, ok := current.Labels[castai.LabelNodeID]; ok {
			if val != "" && val != req.NodeID {
				log.Infof("node id mismatch, expected %q got %q. Skipping delete.", req.NodeID, val)
				return errNodeMismatch
			}
		}

		err = h.clientset.CoreV1().Nodes().Delete(ctx, current.Name, metav1.DeleteOptions{})
		if apierrors.IsNotFound(err) {
			log.Info("node not found, skipping delete")
			return nil
		}
		return err
	}, b)

	if errors.Is(err, errNodeMismatch) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("error removing node %w", err)
	}

	podsListing := backoff.WithContext(backoff.WithMaxRetries(backoff.NewConstantBackOff(h.cfg.podsTerminationWait), h.cfg.deleteRetries), ctx)
	var pods []v1.Pod
	err = backoff.Retry(func() error {
		podList, err := h.clientset.CoreV1().Pods(metav1.NamespaceAll).List(ctx, metav1.ListOptions{
			FieldSelector: fields.SelectorFromSet(fields.Set{"spec.nodeName": req.NodeName}).String(),
		})
		if err != nil {
			return err
		}
		pods = podList.Items
		return nil

	}, podsListing)

	if err != nil {
		return fmt.Errorf("listing node pods %w", err)
	}

	log.Infof("node has %d pods - removing", len(pods))

	// Create delete options with grace period 0 - force delete.
	deleteOptions := metav1.NewDeleteOptions(0)
	deletePod := func(ctx context.Context, pod v1.Pod) error {
		return h.deletePod(ctx, *deleteOptions, pod)
	}

	if err := h.sendPodsRequests(ctx, pods, deletePod); err != nil {
		return fmt.Errorf("sending delete pods requests: %w", err)
	}

	// Cleanup of pods for which node has been removed. It should take a few seconds but added retry in case of network errors.
	podsWait := backoff.WithContext(backoff.WithMaxRetries(backoff.NewConstantBackOff(h.cfg.podsTerminationWait), h.cfg.deleteRetries), ctx)
	return backoff.Retry(func() error {
		pods, err := h.clientset.CoreV1().Pods(metav1.NamespaceAll).List(ctx, metav1.ListOptions{
			FieldSelector: fields.SelectorFromSet(fields.Set{"spec.nodeName": req.NodeName}).String(),
		})
		if err != nil {
			return fmt.Errorf("unable to list pods for node %q err: %w", req.NodeName, err)
		}
		if len(pods.Items) > 0 {
			return fmt.Errorf("waiting for %d pods to be terminated on node %v", len(pods.Items), req.NodeName)
		}
		return nil
	}, podsWait)
}
