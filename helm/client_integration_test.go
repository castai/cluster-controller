package helm

//
//import (
//	"context"
//	"errors"
//	"io/ioutil"
//	"os"
//	"testing"
//
//	"github.com/sirupsen/logrus"
//	"github.com/stretchr/testify/assert"
//	"helm.sh/helm/v3/pkg/storage/driver"
//)
//
//func TestClientIntegration(t *testing.T) {
//	kubeconfigPath := os.Getenv("ADDONS_KUBECONFIG")
//	if kubeconfigPath == "" {
//		t.Skip()
//	}
//
//	it := assert.New(t)
//
//	kubeconfig, err := ioutil.ReadFile(kubeconfigPath)
//	it.NoError(err)
//
//	client := NewClient(logrus.New(), NewCacheChartLoader())
//
//	t.Run("Install", func(t *testing.T) {
//		rel, err := client.Install(context.Background(), InstallOptions{
//			ArchiveURL: "https://github.com/castai/official-addons/releases/download/kubernetes-dashboard-1.0.0/kubernetes-dashboard-1.0.0.tgz",
//			KubeConfig: kubeconfig,
//			Labels:     nil,
//			ValuesOverrides: map[string]string{
//				"kubernetes-dashboard.replicaCount": "2",
//			},
//		})
//
//		it.NoError(err)
//		it.Equal("kubernetes-dashboard", rel.Name)
//		it.Equal("kubernetes-dashboard", rel.Namespace)
//		it.Equal(int64(2), rel.Config["kubernetes-dashboard"].(map[string]interface{})["replicaCount"])
//	})
//
//	t.Run("Upgrade", func(t *testing.T) {
//		rel, err := client.GetRelease(GetReleaseOptions{
//			KubeConfig:  kubeconfig,
//			ReleaseName: "kubernetes-dashboard",
//		})
//		it.NoError(err)
//		rel, err = client.Upgrade(context.Background(), UpgradeOptions{
//			KubeConfig: kubeconfig,
//			ArchiveURL: "https://github.com/castai/official-addons/releases/download/kubernetes-dashboard-1.0.0/kubernetes-dashboard-1.0.0.tgz",
//			Release:    rel,
//			ValuesOverrides: map[string]string{
//				"kubernetes-dashboard.replicaCount": "3",
//			},
//		})
//
//		it.NoError(err)
//		it.Equal("kubernetes-dashboard", rel.Name)
//		it.Equal("kubernetes-dashboard", rel.Namespace)
//		it.Equal(int64(3), rel.Config["kubernetes-dashboard"].(map[string]interface{})["replicaCount"])
//	})
//
//	t.Run("Uninstall", func(t *testing.T) {
//		err = client.Uninstall(UninstallOptions{
//			KubeConfig:  kubeconfig,
//			ReleaseName: "kubernetes-dashboard",
//		})
//		it.NoError(err)
//
//		_, err := client.GetRelease(GetReleaseOptions{
//			KubeConfig:  kubeconfig,
//			ReleaseName: "kubernetes-dashboard",
//		})
//		it.True(errors.Is(err, driver.ErrReleaseNotFound))
//	})
//}
