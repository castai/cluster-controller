package loadtest

import (
	"fmt"

	"github.com/samber/lo"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	DefaultKwokMarker = "kwok.x-k8s.io/node"
	KwokMarkerValue   = "fake-node"
)

// Kwok wraps
type Kwok struct {
}

type KwokConfig struct {
	// Label should match what kwok is configured to use via --manage-nodes-with-label-selector
	// Default is DefaultKwokMarker. Value is always KwokMarkerValue.
	Label string

	// Annotation should match what kwok is configured to use via --manage-nodes-with-annotation-selector
	// Default is DefaultKwokMarker. Value is always KwokMarkerValue.
	Annotation string
}

// Should be able to create kwok-owned nodes
// And scheduled deployments, etc on them.

// Run fake server + kwok controller
// Either as two processes or as two container in the same deployment.

// NewKwokNode creates a fake node with reasonable defaults.
// Can be customized but the marker label/annotation must be present.
// Tainted by default with DefaultKwokMarker to avoid running real pods on it.
// Requires that a kwok-controller is running to actually simulate the node on apply.
func NewKwokNode(cfg KwokConfig, nodeName string) *corev1.Node {
	kwokLabel := DefaultKwokMarker
	if cfg.Label != "" {
		kwokLabel = cfg.Label
	}
	kwokAnnotation := DefaultKwokMarker
	if cfg.Annotation != "" {
		kwokAnnotation = cfg.Annotation
	}

	defaultLabels := map[string]string{
		kwokLabel:              KwokMarkerValue,
		corev1.LabelArchStable: "amd64",
		corev1.LabelHostname:   nodeName,
		corev1.LabelOSStable:   "linux",
		"kubernetes.io/role":   "fake-node",
		"type":                 "kwok",
	}

	defaultAnnotations := map[string]string{
		kwokAnnotation: KwokMarkerValue,
	}

	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:        nodeName,
			Labels:      defaultLabels,
			Annotations: defaultAnnotations,
		},
		Spec: corev1.NodeSpec{
			Taints: []corev1.Taint{
				{
					Key:    DefaultKwokMarker,
					Value:  KwokMarkerValue,
					Effect: corev1.TaintEffectNoSchedule,
				},
			},
		},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(32, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(256*1024*1024*1024, resource.BinarySI),
				corev1.ResourcePods:   *resource.NewQuantity(110, resource.DecimalSI),
			},
			Capacity: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(32, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(256*1024*1024*1024, resource.BinarySI),
				corev1.ResourcePods:   *resource.NewQuantity(110, resource.DecimalSI),
			},
			NodeInfo: corev1.NodeSystemInfo{
				KubeletVersion:  "kwok",
				OperatingSystem: "linux",
				Architecture:    "amd64",
			},
			Phase: corev1.NodeRunning,
		},
	}
}

func ForceDeploymentOnNode(deployment *appsv1.Deployment, nodeName string) {
	deployment.Spec.Template.Spec.NodeName = nodeName
}

// DeploymentWithStuckPDB creates a 1-replica deployment with "stuck PDB" that is never satisfiable, i.e. no pods can be evicted.
// Deployment cannot run in reality, it uses fake container.
// Deployment deploys on kwok fake nodes by default.
func DeploymentWithStuckPDB(deploymentName string) (*appsv1.Deployment, *policyv1.PodDisruptionBudget) {
	labelApp := "appname"
	labelValue := fmt.Sprintf("%s-stuck-pdb", deploymentName)

	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      deploymentName,
			Namespace: metav1.NamespaceDefault,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: lo.ToPtr(int32(1)),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					labelApp: labelValue,
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						labelApp: labelValue,
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "fake-container",
							Image: "does-not-exist",
						},
					},
					Affinity: &corev1.Affinity{
						NodeAffinity: &corev1.NodeAffinity{
							RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
								NodeSelectorTerms: []corev1.NodeSelectorTerm{
									{
										MatchExpressions: []corev1.NodeSelectorRequirement{
											{
												Key:      DefaultKwokMarker,
												Operator: corev1.NodeSelectorOpIn,
												Values:   []string{KwokMarkerValue},
											},
										},
									},
								},
							},
						},
					},
					Tolerations: []corev1.Toleration{
						{
							Key:      DefaultKwokMarker,
							Operator: corev1.TolerationOpExists,
							Effect:   corev1.TaintEffectNoSchedule,
						},
					},
				},
			},
		},
	}

	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%s-pdb", deploymentName),
		},
		Spec: policyv1.PodDisruptionBudgetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					labelApp: labelValue,
				},
			},
			MaxUnavailable: lo.ToPtr(intstr.FromInt32(0)),
		},
	}

	return deployment, pdb
}
