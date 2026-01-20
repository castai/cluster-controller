package scenarios

import (
	"fmt"

	"github.com/samber/lo"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	DefaultKwokMarker = "kwok.x-k8s.io/node"
	KwokMarkerValue   = "fake"

	woopStubCRDName   = "recommendations.autoscaling.cast.ai"
	woopStubCRDGroup  = "autoscaling.cast.ai"
	woopStubCRDPlural = "recommendations"
	woopStubCRDKind   = "Recommendation"
)

type KwokConfig struct {
	// Label should match what kwok is configured to use via --manage-nodes-with-label-selector
	// Default is DefaultKwokMarker. Value is always KwokMarkerValue.
	Label string

	// Annotation should match what kwok is configured to use via --manage-nodes-with-annotation-selector
	// Default is DefaultKwokMarker. Value is always KwokMarkerValue.
	Annotation string
}

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
			ProviderID: nodeName,
		},
		Status: corev1.NodeStatus{
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(4000, resource.DecimalSI),
				corev1.ResourceMemory: *resource.NewQuantity(256*1024*1024*1024, resource.BinarySI),
				corev1.ResourcePods:   *resource.NewQuantity(110, resource.DecimalSI),
			},
			Capacity: corev1.ResourceList{
				corev1.ResourceCPU:    *resource.NewMilliQuantity(4000, resource.DecimalSI),
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

// DeploymentWithStuckPDB creates a 1-replica deployment with "stuck PDB" that is never satisfiable, i.e. no pods can be evicted.
// Deployment cannot run in reality, it uses fake container.
// Deployment deploys on kwok fake nodes by default.
func DeploymentWithStuckPDB(deploymentName string) (*appsv1.Deployment, *policyv1.PodDisruptionBudget) {
	deployment := Deployment(deploymentName)

	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name: fmt.Sprintf("%s-pdb", deploymentName),
		},
		Spec: policyv1.PodDisruptionBudgetSpec{
			Selector:       deployment.Spec.Selector,
			MaxUnavailable: lo.ToPtr(intstr.FromInt32(0)),
		},
	}

	return deployment, pdb
}

// Deployment creates a deployment that can run on kwok nodes in the default namespace.
func Deployment(name string) *appsv1.Deployment {
	labelApp := "appname"
	labelValue := fmt.Sprintf("%s-test-pod", name)

	kwokAffinity, kwokToleration := kwokNodeAffinityAndToleration()

	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
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
						NodeAffinity: kwokAffinity,
					},
					Tolerations: []corev1.Toleration{kwokToleration},
				},
			},
		},
	}
}

// Pod returns a pod that can run on kwok nodes in the default namespace.
func Pod(name string) *corev1.Pod {
	kwokAffinity, kwokToleration := kwokNodeAffinityAndToleration()

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: metav1.NamespaceDefault,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "fake-container",
					Image: "does-not-exist",
				},
			},
			Affinity: &corev1.Affinity{
				NodeAffinity: kwokAffinity,
			},
			Tolerations: []corev1.Toleration{
				kwokToleration,
			},
		},
	}
}

// WoopCRD is a stub CRD similar to workload autoscaler's one.
func WoopCRD() *apiextensionsv1.CustomResourceDefinition {
	return &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: woopStubCRDName,
		},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: woopStubCRDGroup,
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{
				{
					Name:    "v1",
					Served:  true,
					Storage: true,
					Schema: &apiextensionsv1.CustomResourceValidation{
						OpenAPIV3Schema: &apiextensionsv1.JSONSchemaProps{
							Type: "object",
							Properties: map[string]apiextensionsv1.JSONSchemaProps{
								"spec": {
									Type: "object",
									Properties: map[string]apiextensionsv1.JSONSchemaProps{
										"replicas": {Type: "integer"},
										"recommendation": {
											Type: "array",
											Items: &apiextensionsv1.JSONSchemaPropsOrArray{
												Schema: &apiextensionsv1.JSONSchemaProps{
													Type: "object",
													Properties: map[string]apiextensionsv1.JSONSchemaProps{
														"containerName": {Type: "string"},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			Scope: apiextensionsv1.NamespaceScoped,
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Plural:   woopStubCRDPlural,
				Singular: "recommendation",
				Kind:     woopStubCRDKind,
				ListKind: "RecommendationList",
			},
		},
	}
}

// WoopCR creates an instance of the CRD from WoopCRD.
func WoopCR(namespace, name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": fmt.Sprintf("%s/v1", woopStubCRDGroup),
			"kind":       woopStubCRDKind,
			"metadata": map[string]any{
				"name":      name,
				"namespace": namespace,
			},
			"spec": map[string]any{
				"replicas": 10,
				"recommendation": []map[string]any{
					{
						"containerName": "test",
					},
				},
			},
		},
	}
}

// WoopGVR returns the GVR for the CRD from WoopCRD.
func WoopGVR() *schema.GroupVersionResource {
	return &schema.GroupVersionResource{
		Group:    woopStubCRDGroup,
		Version:  "v1",
		Resource: woopStubCRDPlural,
	}
}

func kwokNodeAffinityAndToleration() (*corev1.NodeAffinity, corev1.Toleration) {
	return &corev1.NodeAffinity{
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
		}, corev1.Toleration{
			Key:      DefaultKwokMarker,
			Operator: corev1.TolerationOpExists,
			Effect:   corev1.TaintEffectNoSchedule,
		}
}
