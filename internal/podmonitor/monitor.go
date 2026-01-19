// Package podmonitor provides pod monitoring functionality migrated from pod-pinner.
// It monitors pods with castai.io/target-node annotation and evicts them if they
// violate placement constraints.
package podmonitor

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	listerv1 "k8s.io/client-go/listers/core/v1"
)

const (
	// AnnotationTargetNode is the annotation indicating the target node for a pod.
	AnnotationTargetNode = "castai.io/target-node"
	// LabelNodeDraining is the label indicating a node is being drained.
	LabelNodeDraining = "autoscaling.cast.ai/draining"
)

// Monitor watches pods with target-node annotations and evicts them if they
// violate placement constraints.
type Monitor interface {
	Start(ctx context.Context) error
}

// Config holds configuration for the pod monitor.
type Config struct {
	Enabled  bool
	Interval time.Duration
	Duration time.Duration
}

// NewMonitor creates a new pod monitor.
func NewMonitor(
	log logrus.FieldLogger,
	clientset kubernetes.Interface,
	podLister listerv1.PodLister,
	nodeLister listerv1.NodeLister,
	cfg Config,
) Monitor {
	return &monitor{
		log:        log.WithField("component", "pod-monitor"),
		clientset:  clientset,
		podLister:  podLister,
		nodeLister: nodeLister,
		cfg:        cfg,
		tracked:    make(map[string]time.Time),
	}
}

type monitor struct {
	log        logrus.FieldLogger
	clientset  kubernetes.Interface
	podLister  listerv1.PodLister
	nodeLister listerv1.NodeLister
	cfg        Config
	mu         sync.Mutex
	tracked    map[string]time.Time // pod key -> first seen time
}

func (m *monitor) Start(ctx context.Context) error {
	if !m.cfg.Enabled {
		m.log.Info("pod monitor is disabled")
		return nil
	}

	m.log.WithFields(logrus.Fields{
		"interval": m.cfg.Interval,
		"duration": m.cfg.Duration,
	}).Info("starting pod monitor")

	ticker := time.NewTicker(m.cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			m.log.Info("pod monitor stopped")
			return nil
		case <-ticker.C:
			m.checkPods(ctx)
		}
	}
}

func (m *monitor) checkPods(ctx context.Context) {
	pods, err := m.podLister.List(labels.Everything())
	if err != nil {
		m.log.WithError(err).Error("failed to list pods")
		return
	}

	now := time.Now()

	for _, pod := range pods {
		targetNode, ok := pod.Annotations[AnnotationTargetNode]
		if !ok {
			continue
		}

		podKey := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
		log := m.log.WithFields(logrus.Fields{
			"pod_namespace": pod.Namespace,
			"pod_name":      pod.Name,
			"target_node":   targetNode,
		})

		// Track when we first saw this pod
		m.mu.Lock()
		firstSeen, exists := m.tracked[podKey]
		if !exists {
			m.tracked[podKey] = now
			firstSeen = now
			log.Debug("started monitoring pod")
		}
		m.mu.Unlock()

		// Check if monitoring duration has expired
		if now.Sub(firstSeen) > m.cfg.Duration {
			m.mu.Lock()
			delete(m.tracked, podKey)
			m.mu.Unlock()
			log.Debug("monitoring duration expired for pod")
			continue
		}

		// Run checks
		violations := m.checkPod(pod, targetNode)
		if len(violations) == 0 {
			// Pod is healthy, stop tracking
			m.mu.Lock()
			delete(m.tracked, podKey)
			m.mu.Unlock()
			log.Debug("pod passed all checks, stopping monitoring")
			continue
		}

		// Check for permanent violations
		hasPermanent := false
		for _, v := range violations {
			if v.Permanent {
				hasPermanent = true
				break
			}
		}

		if hasPermanent {
			log.WithField("violations", violationsToStrings(violations)).Warn("pod has permanent violations, evicting")
			if err := m.evictPod(ctx, pod); err != nil {
				log.WithError(err).Error("failed to evict pod")
			} else {
				log.Info("pod evicted successfully")
			}
			m.mu.Lock()
			delete(m.tracked, podKey)
			m.mu.Unlock()
		} else {
			log.WithField("violations", violationsToStrings(violations)).Debug("pod has transient violations, will retry")
		}
	}

	// Clean up tracked pods that no longer exist
	m.cleanupTracked(pods)
}

func (m *monitor) checkPod(pod *corev1.Pod, targetNode string) []Violation {
	var violations []Violation

	// Check 1: Pod is scheduled
	if v := CheckScheduled(pod); v != nil {
		violations = append(violations, *v)
	}

	// Check 2: Pod is on correct node
	if v := CheckCorrectNode(pod, targetNode); v != nil {
		violations = append(violations, *v)
	}

	// Check 3: Target node is not draining
	node, err := m.nodeLister.Get(targetNode)
	if err == nil {
		if v := CheckNotDraining(node); v != nil {
			violations = append(violations, *v)
		}
	}

	return violations
}

func (m *monitor) evictPod(ctx context.Context, pod *corev1.Pod) error {
	eviction := &policyv1.Eviction{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pod.Name,
			Namespace: pod.Namespace,
		},
	}

	err := m.clientset.PolicyV1().Evictions(pod.Namespace).Evict(ctx, eviction)
	if apierrors.IsNotFound(err) {
		return nil // Pod already gone
	}
	return err
}

func (m *monitor) cleanupTracked(currentPods []*corev1.Pod) {
	podSet := make(map[string]struct{})
	for _, pod := range currentPods {
		podKey := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
		podSet[podKey] = struct{}{}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for podKey := range m.tracked {
		if _, exists := podSet[podKey]; !exists {
			delete(m.tracked, podKey)
		}
	}
}

func violationsToStrings(violations []Violation) []string {
	result := make([]string, len(violations))
	for i, v := range violations {
		result[i] = v.Description
	}
	return result
}
