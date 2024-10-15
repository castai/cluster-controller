package hook

import (
	"bytes"
	"fmt"
	"testing"
	"text/template"
	"time"

	"github.com/stretchr/testify/require"
	"helm.sh/helm/v3/pkg/release"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/castai/cluster-controller/internal/helm/hook/mock"
)

type componentVersions struct {
	appVersion      string
	chartVersion    string
	newAppVersion   string
	newChartVersion string
}

type k8sObjectDetails struct {
	apiVersion   string
	updateLabels bool
}

func renderManifestTemplate(apiVersion string, kind string, name string, appVersion string, chartVersion string) (string, error) {
	vars := map[string]interface{}{
		"ApiVersion":   apiVersion,
		"Kind":         kind,
		"Name":         name,
		"AppVersion":   appVersion,
		"ChartVersion": chartVersion,
	}

	manifestTemplate := `---
apiVersion: {{ .ApiVersion }}
kind: {{ .Kind}}
metadata:
  labels:
    app.kubernetes.io/instance: {{ .Name }}
    app.kubernetes.io/managed-by: Helm
    app.kubernetes.io/name: {{ .Name }}
    app.kubernetes.io/version: {{ .AppVersion }}
	{{- if .ChartVersion }}
    helm.sh/chart: {{ .Name }}-{{ .ChartVersion }}
    {{- end }}
  name: {{ .Name }}
`

	tmpl, err := template.New("template").Parse(manifestTemplate)
	if err != nil {
		return "", fmt.Errorf("parsing manifest template: %w", err)
	}

	var renderedTemplate bytes.Buffer
	if err := tmpl.Execute(&renderedTemplate, vars); err != nil {
		return "", fmt.Errorf("rendering manifest template: %w", err)
	}

	return renderedTemplate.String(), nil

}

func TestIgnoreHook(t *testing.T) {
	r := require.New(t)

	components := map[string]componentVersions{
		"castai-evictor":            {"0.5.1", "0.10.0", "0.6.0", "0.11.0"},
		"castai-pod-pinner":         {"0.5.1", "0.10.0", "0.6.0", "0.11.0"},
		"castai-agent":              {"0.5.1", "0.10.0", "0.6.0", "0.11.0"},
		"castai-spot-handler":       {"0.5.1", "0.10.0", "0.6.0", "0.11.0"},
		"castai-egressd":            {"0.5.1", "0.10.0", "0.6.0", "0.11.0"},
		"castai-kvisor":             {"0.5.1", "0.10.0", "0.6.0", "0.11.0"},
		"castai-kvisor-runtime":     {"0.5.1", "0.10.0", "0.6.0", "0.11.0"},
		"castai-cluster-controller": {"v0.37.0", "0.52.0", "v0.38.0", "0.53.0"},
	}

	k8sObjects := map[string]k8sObjectDetails{
		"ClusterRoleBinding": {"rbac.authorization.k8s.io/v1", false},
		"ClusterRole":        {"rbac.authorization.k8s.io/v1", false},
		"Role":               {"rbac.authorization.k8s.io/v1", false},
		"RoleBinding":        {"rbac.authorization.k8s.io/v1", false},
		"Service":            {"v1", true},
	}

	// Generate old and new manifest strings.
	var oldManifests, newManifests string
	for name, c := range components {
		for kind, d := range k8sObjects {
			oldM, err := renderManifestTemplate(d.apiVersion, kind, name, c.appVersion, c.chartVersion)
			if err != nil {
				r.Error(err)
			}
			oldManifests += oldM

			newM, err := renderManifestTemplate(d.apiVersion, kind, name, c.newAppVersion, c.newChartVersion)
			if err != nil {
				r.Error(err)
			}
			newManifests += newM
		}
	}

	oldRelease := &release.Release{
		Manifest: oldManifests,
	}

	cl := &mock.MockKubeClient{}

	hook := LabelIgnoreHook{
		oldRelease: oldRelease,
		kubeClient: cl,
	}

	buf := bytes.NewBuffer([]byte(newManifests))

	fixedManifest, err := hook.Run(buf)
	r.NoError(err)

	typed, err := cl.Build(fixedManifest, false)
	r.NoError(err)

	// Iterate through Helm generated k8s objects.
	for _, res := range typed {
		u := res.Object.(*unstructured.Unstructured)

		// Assert all castai-components k8s resources pairs in one place.
		for kind, d := range k8sObjects {
			if u.GetKind() == kind {
				if c, ok := components[u.GetName()]; ok {
					// If labels should have been updated by post render hook - change them for correct assertion.
					appVersion := c.appVersion
					chartVersion := c.chartVersion
					if d.updateLabels {
						appVersion = c.newAppVersion
						chartVersion = c.newChartVersion
					}

					r.Equal(appVersion, u.GetLabels()[k8sVersionLabel])
					r.Equal(fmt.Sprintf("%s-%s", u.GetName(), chartVersion), u.GetLabels()[helmVersionLabel])
				}
			}
		}
	}

	time.Sleep(1 * time.Second)
}
