package helm

import (
	"bytes"
	"context"
	"io/ioutil"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/kube"
	"helm.sh/helm/v3/pkg/kube/fake"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage"
	"helm.sh/helm/v3/pkg/storage/driver"
	"helm.sh/helm/v3/pkg/time"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/castai/cluster-controller/castai"
)

func TestClientInstall(t *testing.T) {
	r := require.New(t)

	client := &client{
		log:                 logrus.New(),
		chartLoader:         &testChartLoader{chart: buildNginxIngressChart()},
		configurationGetter: &testConfigurationGetter{t: t},
	}

	rel, err := client.Install(context.Background(), InstallOptions{
		ReleaseName: "nginx-ingress",
		Namespace:   "test",
		ValuesOverrides: map[string]string{
			"controller.replicaCount": "2",
			"controller.service.type": "NodePort",
			"random":                  "noop",
		},
	})

	r.NoError(err)
	r.Equal("nginx-ingress", rel.Name)
	r.Equal("test", rel.Namespace)
	r.Equal(int64(2), rel.Config["controller"].(map[string]interface{})["replicaCount"])
	r.Equal("NodePort", rel.Config["controller"].(map[string]interface{})["service"].(map[string]interface{})["type"])
	r.Equal("noop", rel.Config["random"])
}

func TestClientUpdate(t *testing.T) {
	r := require.New(t)

	currentRelease := buildNginxIngressRelease(release.StatusDeployed)
	client := &client{
		log:         logrus.New(),
		chartLoader: &testChartLoader{chart: buildNginxIngressChart()},
		configurationGetter: &testConfigurationGetter{
			t:              t,
			currentRelease: currentRelease,
		},
	}

	rel, err := client.Upgrade(context.Background(), UpgradeOptions{
		Release: currentRelease,
		ValuesOverrides: map[string]string{
			"controller.replicaCount": "100",
			"controller.service.type": "NodePort",
			"random":                  "noop",
		},
	})

	r.NoError(err)
	r.NotNil(rel)
	r.Equal("nginx-ingress", rel.Name)
	r.Equal(int64(100), rel.Config["controller"].(map[string]interface{})["replicaCount"])
	r.Equal("noop", rel.Config["random"])
}

func TestClientUninstall(t *testing.T) {
	r := require.New(t)

	currentRelease := buildNginxIngressRelease(release.StatusDeployed)
	client := &client{
		log:         logrus.New(),
		chartLoader: &testChartLoader{chart: buildNginxIngressChart()},
		configurationGetter: &testConfigurationGetter{
			t:              t,
			currentRelease: currentRelease,
		},
	}

	_, err := client.Uninstall(UninstallOptions{
		ReleaseName: currentRelease.Name,
		Namespace:   currentRelease.Namespace,
	})
	r.NoError(err)
}

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

	cl := kube.New(nil)

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
}

type testConfigurationGetter struct {
	t              *testing.T
	currentRelease *release.Release
}

func (c *testConfigurationGetter) Get(_ string) (*action.Configuration, error) {
	cfg := &action.Configuration{
		Releases:     storage.Init(driver.NewMemory()),
		KubeClient:   &fake.PrintingKubeClient{Out: ioutil.Discard},
		Capabilities: chartutil.DefaultCapabilities,
		Log: func(format string, v ...interface{}) {
			c.t.Helper()
			c.t.Logf(format, v...)
		},
	}

	if c.currentRelease != nil {
		if err := cfg.Releases.Create(c.currentRelease); err != nil {
			return nil, err
		}
	}

	return cfg, nil
}

type testChartLoader struct {
	chart *chart.Chart
}

func (t *testChartLoader) Load(_ context.Context, _ *castai.ChartSource) (*chart.Chart, error) {
	return t.chart, nil
}

func buildNginxIngressChart() *chart.Chart {
	return &chart.Chart{
		Metadata: &chart.Metadata{
			APIVersion: "v1",
			Name:       "nginx-ingress",
			Version:    "0.1.0",
		},
		Templates: []*chart.File{
			{Name: "templates/hello", Data: []byte("hello: world")},
		},
		Values: map[string]interface{}{
			"controller": map[string]interface{}{
				"replicaCount": 1,
				"service": map[string]interface{}{
					"type": "LoadBalancer",
				},
			},
		},
	}
}

func buildNginxIngressRelease(status release.Status) *release.Release {
	now := time.Now()
	return &release.Release{
		Name:      "nginx-ingress",
		Namespace: "test",
		Info: &release.Info{
			FirstDeployed: now,
			LastDeployed:  now,
			Status:        status,
			Description:   "Named Release Stub",
		},
		Chart:   buildNginxIngressChart(),
		Config:  map[string]interface{}{"name": "value"},
		Version: 1,
	}
}
