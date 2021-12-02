package helm

//
//import (
//	"context"
//	"io/ioutil"
//	"os"
//	"testing"
//
//	"github.com/sirupsen/logrus"
//	"github.com/stretchr/testify/assert"
//	"helm.sh/helm/v3/pkg/action"
//	"helm.sh/helm/v3/pkg/chart"
//	"helm.sh/helm/v3/pkg/chartutil"
//	"helm.sh/helm/v3/pkg/release"
//	"helm.sh/helm/v3/pkg/storage"
//	"helm.sh/helm/v3/pkg/storage/driver"
//	"helm.sh/helm/v3/pkg/time"
//
//	kubefake "helm.sh/helm/v3/pkg/kube/fake"
//)
//
//func TestClientInstall(t *testing.T) {
//	it := assert.New(t)
//
//	client := &client{
//		log:                 logrus.New(),
//		chartLoader:         &testChartLoader{chart: buildNginxIngressChart()},
//		configurationGetter: &testConfigurationGetter{t: t},
//	}
//
//	rel, err := client.Install(context.Background(), InstallOptions{
//		ArchiveURL: "https://some-repo/nginx-ingress-1.0.0.tgz",
//		KubeConfig: []byte("fake"),
//		Labels:     nil,
//		ValuesOverrides: map[string]string{
//			"controller.replicaCount": "2",
//			"controller.service.type": "NodePort",
//			"random":                  "noop",
//		},
//	})
//
//	it.NoError(err)
//	it.Equal("nginx-ingress", rel.Name)
//	it.Equal("nginx-ingress", rel.Namespace)
//	it.Equal(int64(2), rel.Config["controller"].(map[string]interface{})["replicaCount"])
//	it.Equal("NodePort", rel.Config["controller"].(map[string]interface{})["service"].(map[string]interface{})["type"])
//	it.Equal("noop", rel.Config["random"])
//}
//
//func TestClientUpdate(t *testing.T) {
//	it := assert.New(t)
//
//	currentRelease := buildNginxIngressRelease(release.StatusDeployed)
//	client := &client{
//		log:                 logrus.New(),
//		chartLoader:         &testChartLoader{chart: buildNginxIngressChart()},
//		configurationGetter: &testConfigurationGetter{t: t, currentRelease: currentRelease},
//	}
//
//	rel, err := client.Upgrade(context.Background(), UpgradeOptions{
//		Release:    currentRelease,
//		ArchiveURL: "https://some-repo/nginx-ingress-1.0.0.tgz",
//		KubeConfig: []byte("fake"),
//		ValuesOverrides: map[string]string{
//			"controller.replicaCount": "100",
//			"controller.service.type": "NodePort",
//			"random":                  "noop",
//		},
//	})
//
//	it.NoError(err)
//	it.NotNil(rel)
//	it.Equal("nginx-ingress", rel.Name)
//	it.Equal(int64(100), rel.Config["controller"].(map[string]interface{})["replicaCount"])
//	it.Equal("noop", rel.Config["random"])
//}
//
//func TestClientDelete(t *testing.T) {
//	it := assert.New(t)
//
//	currentRelease := buildNginxIngressRelease(release.StatusDeployed)
//	client := &client{
//		log:                 logrus.New(),
//		chartLoader:         &testChartLoader{chart: buildNginxIngressChart()},
//		configurationGetter: &testConfigurationGetter{t: t, currentRelease: currentRelease},
//	}
//
//	err := client.Uninstall(UninstallOptions{
//		ReleaseName: "nginx-ingress",
//		KubeConfig:  []byte("fake"),
//	})
//
//	it.NoError(err)
//}
//
//type testConfigurationGetter struct {
//	t              *testing.T
//	currentRelease *release.Release
//}
//
//func (c *testConfigurationGetter) Get(namespace string, kubeconfig []byte) (*action.Configuration, error) {
//	cfg := actionConfigFixture(c.t)
//	if c.currentRelease != nil {
//		if err := cfg.Releases.Create(c.currentRelease); err != nil {
//			return nil, err
//		}
//	}
//	return cfg, nil
//}
//
//type testChartLoader struct {
//	chart *chart.Chart
//}
//
//func (t *testChartLoader) Load(ctx context.Context, url string) (*chart.Chart, error) {
//	return t.chart, nil
//}
//
//func actionConfigFixture(t *testing.T) *action.Configuration {
//	t.Helper()
//
//	tdir, err := ioutil.TempDir("", "helm-action-test")
//	if err != nil {
//		t.Fatal(err)
//	}
//
//	t.Cleanup(func() { os.RemoveAll(tdir) })
//
//	return &action.Configuration{
//		Releases:     storage.Init(driver.NewMemory()),
//		KubeClient:   &kubefake.FailingKubeClient{PrintingKubeClient: kubefake.PrintingKubeClient{Out: ioutil.Discard}},
//		Capabilities: chartutil.DefaultCapabilities,
//		Log: func(format string, v ...interface{}) {
//			t.Helper()
//			t.Logf(format, v...)
//		},
//	}
//}
//
//func buildNginxIngressChart() *chart.Chart {
//	return &chart.Chart{
//		Metadata: &chart.Metadata{
//			APIVersion: "v1",
//			Name:       "nginx-ingress",
//			Version:    "0.1.0",
//		},
//		Templates: []*chart.File{
//			{Name: "templates/hello", Data: []byte("hello: world")},
//		},
//		Values: map[string]interface{}{
//			"controller": map[string]interface{}{
//				"replicaCount": 1,
//				"service": map[string]interface{}{
//					"type": "LoadBalancer",
//				},
//			},
//		},
//	}
//}
//
//func buildNginxIngressRelease(status release.Status) *release.Release {
//	now := time.Now()
//	return &release.Release{
//		Name: "nginx-ingress",
//		Info: &release.Info{
//			FirstDeployed: now,
//			LastDeployed:  now,
//			Status:        status,
//			Description:   "Named Release Stub",
//		},
//		Chart:   buildNginxIngressChart(),
//		Config:  map[string]interface{}{"name": "value"},
//		Version: 1,
//	}
//}
