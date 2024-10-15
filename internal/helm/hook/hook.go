package hook

import (
	"bytes"
	"fmt"
	"strings"

	"helm.sh/helm/v3/pkg/kube"
	"helm.sh/helm/v3/pkg/release"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

// group/version/kind/namespace/name
var labelIgnoreResources = map[string]struct{}{
	"rbac.authorization.k8s.io/v1/ClusterRole//castai-evictor":        {},
	"rbac.authorization.k8s.io/v1/ClusterRoleBinding//castai-evictor": {},
	"rbac.authorization.k8s.io/v1/Role//castai-evictor":               {},
	"rbac.authorization.k8s.io/v1/RoleBinding//castai-evictor":        {},

	"rbac.authorization.k8s.io/v1/ClusterRole//castai-pod-pinner":        {},
	"rbac.authorization.k8s.io/v1/ClusterRoleBinding//castai-pod-pinner": {},
	"rbac.authorization.k8s.io/v1/Role//castai-pod-pinner":               {},
	"rbac.authorization.k8s.io/v1/RoleBinding//castai-pod-pinner":        {},

	"rbac.authorization.k8s.io/v1/ClusterRole//castai-agent":        {},
	"rbac.authorization.k8s.io/v1/ClusterRoleBinding//castai-agent": {},
	"rbac.authorization.k8s.io/v1/Role//castai-agent":               {},
	"rbac.authorization.k8s.io/v1/RoleBinding//castai-agent":        {},

	"rbac.authorization.k8s.io/v1/ClusterRole//castai-spot-handler":        {},
	"rbac.authorization.k8s.io/v1/ClusterRoleBinding//castai-spot-handler": {},
	"rbac.authorization.k8s.io/v1/Role//castai-spot-handler":               {},
	"rbac.authorization.k8s.io/v1/RoleBinding//castai-spot-handler":        {},

	"rbac.authorization.k8s.io/v1/ClusterRole//castai-egressd":        {},
	"rbac.authorization.k8s.io/v1/ClusterRoleBinding//castai-egressd": {},
	"rbac.authorization.k8s.io/v1/Role//castai-egressd":               {},
	"rbac.authorization.k8s.io/v1/RoleBinding//castai-egressd":        {},

	"rbac.authorization.k8s.io/v1/ClusterRole//castai-kvisor":        {},
	"rbac.authorization.k8s.io/v1/ClusterRoleBinding//castai-kvisor": {},
	"rbac.authorization.k8s.io/v1/Role//castai-kvisor":               {},
	"rbac.authorization.k8s.io/v1/RoleBinding//castai-kvisor":        {},

	"rbac.authorization.k8s.io/v1/ClusterRole//castai-kvisor-runtime":        {},
	"rbac.authorization.k8s.io/v1/ClusterRoleBinding//castai-kvisor-runtime": {},
	"rbac.authorization.k8s.io/v1/Role//castai-kvisor-runtime":               {},
	"rbac.authorization.k8s.io/v1/RoleBinding//castai-kvisor-runtime":        {},

	"rbac.authorization.k8s.io/v1/ClusterRole//castai-cluster-controller":        {},
	"rbac.authorization.k8s.io/v1/ClusterRoleBinding//castai-cluster-controller": {},
	"rbac.authorization.k8s.io/v1/Role//castai-cluster-controller":               {},
	"rbac.authorization.k8s.io/v1/RoleBinding//castai-cluster-controller":        {},
}

const (
	k8sVersionLabel  = "app.kubernetes.io/version"
	helmVersionLabel = "helm.sh/chart"
)

func NewLabelIgnoreHook(kubeClient kube.Interface, oldRelease *release.Release) *LabelIgnoreHook {
	return &LabelIgnoreHook{
		kubeClient: kubeClient,
		oldRelease: oldRelease,
	}
}

// LabelIgnoreHook prevents certain resource getting updated, if only their version labels have changed.
// This is needed in order to update components like evictor with it's own cluster scoped resources like clusterrole.
// Cluster controller can't update these rbac resource since it lacks permissions (unless user configures cluster-admin role).
type LabelIgnoreHook struct {
	kubeClient kube.Interface
	oldRelease *release.Release
}

func (l *LabelIgnoreHook) Run(renderedManifests *bytes.Buffer) (*bytes.Buffer, error) {
	b := bytes.NewBuffer(nil)

	newManifests, err := l.kubeClient.Build(renderedManifests, false)
	if err != nil {
		return nil, err
	}

	oldManifests, err := l.kubeClient.Build(strings.NewReader(l.oldRelease.Manifest), false)
	if err != nil {
		return nil, err
	}

	for _, r := range newManifests {
		u := r.Object.(*unstructured.Unstructured)

		gvk := r.Object.GetObjectKind().GroupVersionKind()
		key := fmt.Sprintf("%s/%s/%s/%s", gvk.GroupVersion().String(), gvk.Kind, r.Namespace, r.Name)

		if _, ok := labelIgnoreResources[key]; ok {
			oldLabels := getChartLabels(oldManifests, u.GetName(), u.GetKind(), u.GetNamespace())
			if oldLabels == nil {
				return nil, fmt.Errorf("updating a previously non-existant chart %s", gvk)
			}
			labelCopy := u.GetLabels()
			// Reset version labels to previous release.
			if v, found := oldLabels[k8sVersionLabel]; found {
				labelCopy[k8sVersionLabel] = v
			}
			if v, found := oldLabels[helmVersionLabel]; found {
				labelCopy[helmVersionLabel] = v
			}
			u.SetLabels(labelCopy)
		}

		js, err := u.MarshalJSON()
		if err != nil {
			return nil, err
		}

		y, err := yaml.JSONToYAML(js)
		if err != nil {
			return nil, err
		}

		_, _ = fmt.Fprintf(b, "---\n%s\n", y)
	}

	return b, nil
}

func getChartLabels(list kube.ResourceList, chartName, kind, namespace string) map[string]string {
	for _, r := range list {
		u := r.Object.(*unstructured.Unstructured)
		if u.GetName() == chartName && u.GetKind() == kind && u.GetNamespace() == namespace {
			return u.GetLabels()
		}
	}

	return nil
}
