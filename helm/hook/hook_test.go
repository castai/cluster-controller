package hook

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"helm.sh/helm/v3/pkg/release"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/castai/cluster-controller/helm/hook/mock"
)

func TestIgnoreHook(t *testing.T) {
	r := require.New(t)

	oldManifests :=
		`---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  labels:
    app.kubernetes.io/instance: castai-evictor
    app.kubernetes.io/managed-by: Helm
    app.kubernetes.io/name: castai-evictor
    app.kubernetes.io/version: 0.5.1
    helm.sh/chart: castai-evictor-0.10.0
  name: castai-evictor

---
apiVersion: v1
kind: Service
metadata:
  labels:
    app.kubernetes.io/instance: castai-evictor
    app.kubernetes.io/managed-by: Helm
    app.kubernetes.io/name: castai-evictor
    app.kubernetes.io/version: 0.5.1
    helm.sh/chart: castai-evictor-0.10.0
  name: castai-evictor
  namespace: castai-agent`

	newManifests :=
		`---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  labels:
    app.kubernetes.io/instance: castai-evictor
    app.kubernetes.io/managed-by: Helm
    app.kubernetes.io/name: castai-evictor
    app.kubernetes.io/version: 0.6.0
    helm.sh/chart: castai-evictor-0.11.0
  name: castai-evictor

---
apiVersion: v1
kind: Service
metadata:
  labels:
    app.kubernetes.io/instance: castai-evictor
    app.kubernetes.io/managed-by: Helm
    app.kubernetes.io/name: castai-evictor
    app.kubernetes.io/version: 0.6.0
    helm.sh/chart: castai-evictor-0.11.0
  name: castai-evictor
  namespace: castai-agent`


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

	for _, res := range typed {
		u := res.Object.(*unstructured.Unstructured)

		if u.GetKind() == "Service" {
			r.Equal("0.6.0", u.GetLabels()[k8sVersionLabel])
			r.Equal("castai-evictor-0.11.0", u.GetLabels()[helmVersionLabel])
		}

		if u.GetKind() == "ClusterRoleBinding" {
			r.Equal("0.5.1", u.GetLabels()[k8sVersionLabel])
			r.Equal("castai-evictor-0.10.0", u.GetLabels()[helmVersionLabel])
		}
	}

	time.Sleep(1 * time.Second)
}
