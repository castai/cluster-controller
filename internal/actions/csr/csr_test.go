package csr

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	certv1 "k8s.io/api/certificates/v1"
	certv1beta1 "k8s.io/api/certificates/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

	err = cert.DeleteCSR(ctx, client)
	r.NoError(err)

	cert, err = cert.NewCSR(ctx, client)
	r.NoError(err)

	_, err = cert.ApproveCSRCertificate(ctx, client)
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
			cert := &Certificate{
				Name: tt.args.subjectCommonName,
			}

			require.Equal(t, tt.want, cert.ForCASTAINode())
		})
	}
}

func Test_outdatedCertificate(t *testing.T) {
	tt := map[string]struct {
		createTimestamp time.Time
		want            bool
	}{
		"Outdated": {
			createTimestamp: time.Now().Add(-csrTTL).Add(-time.Second),
			want:            true,
		},
		"Not outdated": {
			createTimestamp: time.Now(),
			want:            false,
		},
		"Outdated, right before": {
			createTimestamp: time.Now().Add(-csrTTL).Add(2 * time.Second),
			want:            false,
		},
	}

	for name, tc := range tt {
		t.Run(name, func(t *testing.T) {
			cert := &Certificate{
				v1: &certv1.CertificateSigningRequest{
					ObjectMeta: metav1.ObjectMeta{
						CreationTimestamp: metav1.NewTime(tc.createTimestamp),
					},
				},
			}
			require.Equal(t, tc.want, cert.Outdated())

			certBeta := &Certificate{
				v1Beta1: &certv1beta1.CertificateSigningRequest{
					ObjectMeta: metav1.ObjectMeta{
						CreationTimestamp: metav1.NewTime(tc.createTimestamp),
					},
				},
			}
			require.Equal(t, tc.want, certBeta.Outdated())
		})
	}
}

func Test_nodeBootstrap(t *testing.T) {
	tt := map[string]struct {
		reqUser string
		want    bool
	}{
		"other one": {
			reqUser: "dummy-user",
			want:    false,
		},
		"kubelet-bootstrap": {
			reqUser: "kubelet-bootstrap",
			want:    true,
		},
		"castai-cluster-controller": {
			reqUser: "system:serviceaccount:castai-agent:castai-cluster-controller",
			want:    true,
		},
	}

	for name, tc := range tt {
		t.Run(name, func(t *testing.T) {
			cert := &Certificate{
				RequestingUser: tc.reqUser,
			}
			require.Equal(t, tc.want, cert.NodeBootstrap())
		})
	}
}

func Test_toCertificate(t *testing.T) {
	kBootstrapCSRv1 := getCSRv1("node-csr", "kubelet-bootstrap")
	kBootstrapCtCSRv1beta1 := getCSRv1betav1("node-csr", "kubelet-bootstrap")
	kServingCSRv1 := getCSRv1("csr-s7v44", "system:node:gke-va")
	kServingCSRv1beta1 := getCSRv1betav1("csr-s7v44", "system:node:gke-va")
	type args struct {
		obj interface{}
	}
	tests := []struct {
		name      string
		args      args
		checkFunc func(t *testing.T, cert *Certificate)
		wantErr   bool
	}{
		{
			name: "empty event",
			args: args{
				obj: nil,
			},
			wantErr: true,
		},
		{
			name: "outdated event",
			args: args{
				obj: &certv1.CertificateSigningRequest{
					ObjectMeta: metav1.ObjectMeta{
						CreationTimestamp: metav1.Time{Time: time.Now().Add(-csrTTL)},
					},
					Spec: kBootstrapCSRv1.Spec,
				},
			},
			checkFunc: func(t *testing.T, cert *Certificate) {
				require.True(t, cert.Outdated())
			},
			wantErr: false,
		},
		{
			name: "bad owner",
			args: args{
				obj: &certv1.CertificateSigningRequest{
					Spec: certv1.CertificateSigningRequestSpec{
						Username: "test",
					},
					ObjectMeta: metav1.ObjectMeta{
						CreationTimestamp: metav1.Time{Time: time.Now().Add(csrTTL)},
					},
				},
			},
			checkFunc: func(t *testing.T, cert *Certificate) {
				require.False(t, cert.NodeBootstrap())
			},
			wantErr: false,
		},
		{
			name: "kubelet-bootstrap: ok v1",
			args: args{
				obj: kBootstrapCSRv1,
			},
			checkFunc: func(t *testing.T, cert *Certificate) {
				require.Equal(t, "system:node:gke-dev-master-cast-pool-cb53177b", cert.Name)
				require.Equal(t, "kubelet-bootstrap", cert.RequestingUser)
				require.Equal(t, kBootstrapCSRv1, cert.v1)
			},
			wantErr: false,
		},
		{
			name: "kubelet-bootstrap: ok v1beta1",
			args: args{
				obj: kBootstrapCtCSRv1beta1,
			},
			wantErr: false,
			checkFunc: func(t *testing.T, cert *Certificate) {
				require.Equal(t, "system:node:gke-dev-master-cast-pool-cb53177b", cert.Name)
				require.Equal(t, "kubelet-bootstrap", cert.RequestingUser)
				require.Equal(t, kBootstrapCtCSRv1beta1, cert.v1Beta1)
			},
		},
		{
			name: "kubelet-serving: ok v1",
			args: args{
				obj: kServingCSRv1,
			},
			checkFunc: func(t *testing.T, cert *Certificate) {
				require.Equal(t, "system:node:gke-dev-master-cast-pool-cb53177b", cert.Name)
				require.Equal(t, "system:node:gke-va", cert.RequestingUser)
				require.Equal(t, kServingCSRv1, cert.v1)
			},
			wantErr: false,
		},
		{
			name: "kubelet-serving: ok v1beta1",
			args: args{
				obj: kServingCSRv1beta1,
			},
			wantErr: false,
			checkFunc: func(t *testing.T, cert *Certificate) {
				require.Equal(t, "system:node:gke-dev-master-cast-pool-cb53177b", cert.Name)
				require.Equal(t, "system:node:gke-va", cert.RequestingUser)
				require.Equal(t, kServingCSRv1beta1, cert.v1Beta1)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotCert, err := toCertificate(tt.args.obj)
			if (err != nil) != tt.wantErr {
				t.Errorf("toCertificate() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if tt.checkFunc != nil {
				tt.checkFunc(t, gotCert)
			}
		})
	}
}
