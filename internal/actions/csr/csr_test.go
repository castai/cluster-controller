package csr

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	certv1 "k8s.io/api/certificates/v1"
	certv1beta1 "k8s.io/api/certificates/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
)

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
			require.Equal(t, tc.want, cert.isRequestedByNodeBootstrap())
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
						Name:              "node-csr-gke-dev-master-cast-pool-cb53177b",
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
			wantErr: true,
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

func TestCertificate_validateCSR(t *testing.T) {
	type fields struct {
		v1              *certv1.CertificateSigningRequest
		v1Beta1         *certv1beta1.CertificateSigningRequest
		Name            string
		OriginalCSRName string
		RequestingUser  string
		SignerName      string
		Usages          []string
	}
	type args struct {
		csr *x509.CertificateRequest
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		wantErr bool
	}{
		{
			name: "empty",
			fields: fields{
				SignerName: certv1.KubeletServingSignerName,
			},
			wantErr: true,
		},
		{
			name: "empty signer",
			fields: fields{
				SignerName: "",
			},
			args: args{
				csr: &x509.CertificateRequest{},
			},
			wantErr: true,
		},
		{
			name: "no validation",
			fields: fields{
				SignerName: certv1.KubeAPIServerClientKubeletSignerName,
			},
			args: args{
				csr: &x509.CertificateRequest{},
			},
		},
		{
			name: "empty sn for serving CSR",
			fields: fields{
				SignerName: certv1.KubeletServingSignerName,
			},
			args: args{
				csr: &x509.CertificateRequest{},
			},
			wantErr: true,
		},
		{
			name: "not empty URI for serving CSR",
			fields: fields{
				SignerName: certv1.KubeletServingSignerName,
			},
			args: args{
				csr: &x509.CertificateRequest{
					Subject: pkix.Name{
						CommonName: "system:node:node1",
					},
					URIs: []*url.URL{
						{}, {},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "not empty Emails for serving CSR",
			fields: fields{
				SignerName: certv1.KubeletServingSignerName,
			},
			args: args{
				csr: &x509.CertificateRequest{
					Subject: pkix.Name{
						CommonName: "system:node:node1",
					},
					EmailAddresses: []string{
						"test",
					},
				},
			},
			wantErr: true,
		},
		{
			name: "empty Usages for serving CSR",
			fields: fields{
				SignerName: certv1.KubeletServingSignerName,
			},
			args: args{
				csr: &x509.CertificateRequest{
					Subject: pkix.Name{
						CommonName: "system:node:node1",
					},
					EmailAddresses: []string{},
				},
			},
			wantErr: true,
		},
		{
			name: "wrong usages for serving CSR",
			fields: fields{
				SignerName: certv1.KubeletServingSignerName,
				Usages:     []string{fmt.Sprintf("%v", certv1.UsageServerAuth), "wrong"},
			},
			args: args{
				csr: &x509.CertificateRequest{
					Subject: pkix.Name{
						CommonName: "system:node:node1",
					},
					EmailAddresses: []string{},
				},
			},
			wantErr: true,
		},
		{
			name: "wrong usages: no server auth for serving CSR",
			fields: fields{
				SignerName: certv1.KubeletServingSignerName,
				Usages:     []string{fmt.Sprintf("%v", certv1.UsageDigitalSignature)},
			},
			args: args{
				csr: &x509.CertificateRequest{
					Subject: pkix.Name{
						CommonName: "system:node:node1",
					},
					EmailAddresses: []string{},
				},
			},
			wantErr: true,
		},
		{
			name: "ok for serving CSR",
			fields: fields{
				SignerName: certv1.KubeletServingSignerName,
				Usages:     []string{fmt.Sprintf("%v", certv1.UsageServerAuth)},
			},
			args: args{
				csr: &x509.CertificateRequest{
					Subject: pkix.Name{
						CommonName: "system:node:node1",
					},
					EmailAddresses: []string{},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Certificate{
				v1:              tt.fields.v1,
				v1Beta1:         tt.fields.v1Beta1,
				Name:            tt.fields.Name,
				OriginalCSRName: tt.fields.OriginalCSRName,
				RequestingUser:  tt.fields.RequestingUser,
				SignerName:      tt.fields.SignerName,
				Usages:          tt.fields.Usages,
			}
			if err := c.validateCSR(tt.args.csr); (err != nil) != tt.wantErr {
				t.Errorf("validateCSR() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
