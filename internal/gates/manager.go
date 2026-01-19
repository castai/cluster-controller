// Package gates provides functionality for managing Kubernetes scheduling gates.
package gates

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	listerv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
)

const (
	// AnnotationTargetNode is the annotation on pods indicating their target node.
	AnnotationTargetNode = "castai.io/target-node"
	// AnnotationGateRemovedAt is the annotation added when the scheduling gate is removed.
	AnnotationGateRemovedAt = "castai.io/gate-removed-at"
	// SchedulingGateName is the name of the CAST AI scheduling gate.
	SchedulingGateName = "castai.io/node-ready"
)

// Manager handles scheduling gate removal for pods targeting ready nodes.
type Manager interface {
	Start(ctx context.Context) error
	OnNodeReady(nodeName string) error
}

// Config holds configuration for the GateManager.
type Config struct {
	Enabled bool
}

// NewManager creates a new GateManager.
func NewManager(
	log logrus.FieldLogger,
	clientset kubernetes.Interface,
	nodeInformer cache.SharedIndexInformer,
	podLister listerv1.PodLister,
	cfg Config,
) Manager {
	return &manager{
		log:          log.WithField("component", "gate-manager"),
		clientset:    clientset,
		nodeInformer: nodeInformer,
		podLister:    podLister,
		cfg:          cfg,
		nodeStates:   make(map[string]bool),
	}
}

type manager struct {
	log          logrus.FieldLogger
	clientset    kubernetes.Interface
	nodeInformer cache.SharedIndexInformer
	podLister    listerv1.PodLister
	cfg          Config
	mu           sync.RWMutex
	nodeStates   map[string]bool
}

func (m *manager) Start(ctx context.Context) error {
	if !m.cfg.Enabled {
		m.log.Info("scheduling gate manager is disabled")
		return nil
	}

	if m.nodeInformer == nil {
		return fmt.Errorf("node informer is required for gate manager")
	}

	m.log.Info("starting scheduling gate manager")

	_, err := m.nodeInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if node, ok := obj.(*corev1.Node); ok {
				m.handleNodeUpdate(nil, node)
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			oldNode, _ := oldObj.(*corev1.Node)
			if newNode, ok := newObj.(*corev1.Node); ok {
				m.handleNodeUpdate(oldNode, newNode)
			}
		},
	})
	if err != nil {
		return fmt.Errorf("adding node event handler: %w", err)
	}

	m.log.Info("scheduling gate manager started successfully")
	return nil
}

func (m *manager) OnNodeReady(nodeName string) error {
	if !m.cfg.Enabled {
		return nil
	}

	log := m.log.WithField("node", nodeName)
	log.Info("node became ready, checking for gated pods")

	pods, err := m.podLister.List(labels.Everything())
	if err != nil {
		return fmt.Errorf("listing pods: %w", err)
	}

	for _, pod := range pods {
		if m.shouldRemoveGate(pod, nodeName) {
			if err := m.removeGate(context.Background(), pod); err != nil {
				log.WithError(err).WithFields(logrus.Fields{
					"pod_namespace": pod.Namespace,
					"pod_name":      pod.Name,
				}).Warn("failed to remove scheduling gate from pod")
			}
		}
	}
	return nil
}

func (m *manager) handleNodeUpdate(oldNode, newNode *corev1.Node) {
	wasReady := oldNode != nil && isNodeReady(oldNode)
	isReady := isNodeReady(newNode)

	m.mu.Lock()
	m.nodeStates[newNode.Name] = isReady
	m.mu.Unlock()

	// Trigger gate removal when node transitions to ready
	if !wasReady && isReady {
		if err := m.OnNodeReady(newNode.Name); err != nil {
			m.log.WithError(err).WithField("node", newNode.Name).Error("failed to process node ready event")
		}
	}
}

func (m *manager) shouldRemoveGate(pod *corev1.Pod, nodeName string) bool {
	// Check if pod targets this node
	target, ok := pod.Annotations[AnnotationTargetNode]
	if !ok || target != nodeName {
		return false
	}

	// Check if pod has our scheduling gate
	return hasSchedulingGate(pod, SchedulingGateName)
}

func (m *manager) removeGate(ctx context.Context, pod *corev1.Pod) error {
	log := m.log.WithFields(logrus.Fields{
		"pod_namespace": pod.Namespace,
		"pod_name":      pod.Name,
	})

	patch := buildGateRemovalPatch(pod, SchedulingGateName)
	if patch == nil {
		return nil
	}

	patchBytes, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("marshaling patch: %w", err)
	}

	log.Info("removing scheduling gate from pod")
	_, err = m.clientset.CoreV1().Pods(pod.Namespace).Patch(
		ctx,
		pod.Name,
		types.StrategicMergePatchType,
		patchBytes,
		metav1.PatchOptions{},
	)
	if apierrors.IsNotFound(err) {
		return nil // Pod no longer exists
	}
	return err
}

func isNodeReady(node *corev1.Node) bool {
	for _, cond := range node.Status.Conditions {
		if cond.Type == corev1.NodeReady {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}

func hasSchedulingGate(pod *corev1.Pod, name string) bool {
	for _, g := range pod.Spec.SchedulingGates {
		if g.Name == name {
			return true
		}
	}
	return false
}

func buildGateRemovalPatch(pod *corev1.Pod, gateName string) map[string]interface{} {
	if !hasSchedulingGate(pod, gateName) {
		return nil
	}

	// Build new gates list without our gate
	var gates []map[string]string
	for _, g := range pod.Spec.SchedulingGates {
		if g.Name != gateName {
			gates = append(gates, map[string]string{"name": g.Name})
		}
	}

	// Copy existing annotations and add removal timestamp
	ann := make(map[string]string)
	for k, v := range pod.Annotations {
		ann[k] = v
	}
	ann[AnnotationGateRemovedAt] = time.Now().UTC().Format(time.RFC3339)

	return map[string]interface{}{
		"metadata": map[string]interface{}{
			"annotations": ann,
		},
		"spec": map[string]interface{}{
			"schedulingGates": gates,
		},
	}
}
