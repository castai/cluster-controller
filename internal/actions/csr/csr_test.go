package csr

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	certv1 "k8s.io/api/certificates/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
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

func Test_toCertificate(t *testing.T) {
	type args struct {
		event watch.Event
	}
	tests := []struct {
		name        string
		args        args
		wantCert    *Certificate
		wantName    string
		wantRequest []byte
		wantErr     bool
	}{
		{
			name: "empty event",
			args: args{
				event: watch.Event{},
			},
			wantErr: true,
		},
		{
			name: "outdated event",
			args: args{
				event: watch.Event{
					Object: &certv1.CertificateSigningRequest{
						ObjectMeta: metav1.ObjectMeta{
							CreationTimestamp: metav1.Time{Time: time.Now().Add(-csrTTL)},
						},
					},
				},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotCert, gotName, gotRequest, err := toCertificate(tt.args.event)
			if (err != nil) != tt.wantErr {
				t.Errorf("toCertificate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(gotCert, tt.wantCert) {
				t.Errorf("toCertificate() gotCert = %v, want %v", gotCert, tt.wantCert)
			}
			if gotName != tt.wantName {
				t.Errorf("toCertificate() gotName = %v, want %v", gotName, tt.wantName)
			}
			if !reflect.DeepEqual(gotRequest, tt.wantRequest) {
				t.Errorf("toCertificate() gotRequest = %v, want %v", gotRequest, tt.wantRequest)
			}
		})
	}
}
