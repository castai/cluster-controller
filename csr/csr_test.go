package csr

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

func TestApproveCSR(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}

	r := require.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client, err := getClient()
	r.NoError(err)

	cert, err := GetCertificateByNodeName(ctx, client, "gke-csr-cast-pool-ab259afb")
	r.NoError(err)

	err = DeleteCertificate(ctx, client, cert)
	r.NoError(err)

	cert, err = RequestCertificate(ctx, client, cert)
	r.NoError(err)

	_, err = ApproveCertificate(ctx, client, cert)
	r.NoError(err)
}

func getClient() (*kubernetes.Clientset, error) {
	var kubeconfig string
	if home := homedir.HomeDir(); home != "" {
		kubeconfig = filepath.Join(home, ".kube", "config")
	}

	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		return nil, err
	}

	clientset, err := kubernetes.NewForConfig(config)
	return clientset, err
}
