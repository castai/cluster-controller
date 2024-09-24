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

	err = cert.DeleteCertificate(ctx, client)
	r.NoError(err)

	cert, err = cert.NewCSR(ctx, client)
	r.NoError(err)

	_, err = cert.ApproveCertificate(ctx, client)
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

func Test_isCastAINodeCsr(t *testing.T) {
	type args struct {
		tuneMockErr       error
		subjectCommonName string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "empty node name",
			want: false,
		},
		{
			name: "not cast in subjectComma	",
			args: args{
				subjectCommonName: "system:node:node1",
			},
			want: false,
		},
		{
			name: "not CastAI node",
			args: args{
				subjectCommonName: "node1",
			},
			want: false,
		},
		{
			name: "CAST AI node",
			args: args{
				subjectCommonName: "system:node:node1-cast-pool-123",
			},
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isCastAINodeCsr(tt.args.subjectCommonName)
			require.Equal(t, tt.want, got)
		})
	}
}
