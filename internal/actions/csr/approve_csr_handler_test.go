package csr

import (
	"context"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	certv1 "k8s.io/api/certificates/v1"
	certv1beta1 "k8s.io/api/certificates/v1beta1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	ktest "k8s.io/client-go/testing"

	"github.com/castai/cluster-controller/internal/actions/csr/test"
	"github.com/castai/cluster-controller/internal/actions/csr/wrapper"
)

func TestApproveCSRHandler(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)

	t.Run("do not process approved v1 csr successfully", func(t *testing.T) {
		r := require.New(t)

		csrRes := getCSR()
		client := fake.NewClientset(csrRes)
		am := NewApprovalManager(log, client)
		csrRes.Status.Conditions = []certv1.CertificateSigningRequestCondition{
			{
				Type: certv1.CertificateApproved,
			},
		}

		ctx := context.Background()
		csr, err := wrapper.NewCSR(client, csrRes)
		r.NoError(err)
		err = am.handle(ctx, log, csr)
		r.NoError(err)
	})

	t.Run("retry getting initial csr", func(t *testing.T) {
		r := require.New(t)

		csrRes := getCSR()
		count := 0
		fn := ktest.ReactionFunc(func(action ktest.Action) (handled bool, ret runtime.Object, err error) {
			if count == 0 {
				count++
				return true, nil, errors.New("api server timeout")
			}
			out := certv1.CertificateSigningRequestList{Items: []certv1.CertificateSigningRequest{*csrRes}}
			return true, &out, err
		})
		client := fake.NewClientset(csrRes)
		client.PrependReactor("list", "certificatesigningrequests", fn)

		am := NewApprovalManager(log, client)

		csr, err := wrapper.NewCSR(client, csrRes)
		r.NoError(err)
		err = am.handle(context.Background(), log, csr)
		r.NoError(err)
	})

	t.Run("approve v1beta1 csr successfully", func(t *testing.T) {
		r := require.New(t)

		signer := certv1beta1.KubeAPIServerClientKubeletSignerName
		csrRes := &certv1beta1.CertificateSigningRequest{
			TypeMeta: metav1.TypeMeta{
				APIVersion: certv1beta1.SchemeGroupVersion.String(),
				Kind:       "CertificateSigningRequest",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:              "node-csr-123",
				CreationTimestamp: metav1.Now(),
			},
			Spec: certv1beta1.CertificateSigningRequestSpec{
				Request: test.NewEncodedCertificateRequest(t, &x509.CertificateRequest{
					Subject: pkix.Name{
						CommonName:   "system:node:gke-am-gcp-cast-pool-5dc4f4ec",
						Organization: []string{"system:nodes"},
					},
				}),
				Username:   "kubelet-bootstrap",
				Usages:     []certv1beta1.KeyUsage{certv1beta1.UsageClientAuth},
				SignerName: &signer,
			},
		}
		client := fake.NewClientset(csrRes)
		// Return NotFound for all v1 resources.
		client.PrependReactor("*", "*", ktest.ReactionFunc {
			if action.GetResource().Version == "v1" {
				err := apierrors.NewNotFound(schema.GroupResource{}, action.GetResource().String())
				return true, nil, err
			}
			return false, nil, nil
		})
		client.PrependReactor("update", "certificatesigningrequests", func(action ktest.Action) (handled bool, ret runtime.Object, err error) {
			approved := csrRes.DeepCopy()
			approved.Status.Conditions = []certv1beta1.CertificateSigningRequestCondition{
				{
					Type:           certv1beta1.CertificateApproved,
					Reason:         "ReasonApproved",
					Message:        "approved",
					LastUpdateTime: metav1.Now(),
					Status:         v1.ConditionTrue,
				},
			}
			return true, approved, nil
		})

		am := NewApprovalManager(log, client)
		csr, err := wrapper.NewCSR(client, csrRes)
		r.NoError(err)
		err = am.handle(context.Background(), log, csr)
		r.NoError(err)
		r.True(csr.Approved())
	})

	t.Run("approve v1beta1 csr failed", func(t *testing.T) {
		r := require.New(t)

		signer := certv1beta1.KubeAPIServerClientKubeletSignerName
		csrRes := &certv1beta1.CertificateSigningRequest{
			TypeMeta: metav1.TypeMeta{
				APIVersion: certv1beta1.SchemeGroupVersion.String(),
				Kind:       "CertificateSigningRequest",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:              "node-csr-123",
				CreationTimestamp: metav1.Now(),
			},
			Spec: certv1beta1.CertificateSigningRequestSpec{
				Request: test.NewEncodedCertificateRequest(t, &x509.CertificateRequest{
					Subject: pkix.Name{
						CommonName:   "system:node:gke-am-gcp-cast-pool-5dc4f4ec",
						Organization: []string{"system:nodes"},
					},
				}),
				Username:   "kubelet-bootstrap",
				Usages:     []certv1beta1.KeyUsage{certv1beta1.UsageClientAuth},
				SignerName: &signer,
			},
		}
		client := fake.NewClientset(csrRes)
		// Return NotFound for all v1 resources.
		client.PrependReactor("*", "*", ktest.ReactionFunc {
			if action.GetResource().Version == "v1" {
				err := apierrors.NewNotFound(schema.GroupResource{}, action.GetResource().String())
				return true, nil, err
			}
			return false, nil, nil
		})
		client.PrependReactor("update", "certificatesigningrequests", ktest.ReactionFunc {
			c := csrRes.DeepCopy()
			c.Status.Conditions = []certv1beta1.CertificateSigningRequestCondition{
				{
					Type:           certv1beta1.CertificateApproved,
					Message:        "error",
					LastUpdateTime: metav1.Now(),
					Status:         v1.ConditionFalse,
				},
			}
			return true, c, nil
		})

		am := NewApprovalManager(log, client)
		csr, err := wrapper.NewCSR(client, csrRes)
		r.NoError(err)
		err = am.handle(context.Background(), log, csr)
		r.Error(err)
		r.False(csr.Approved())
	})
}

func getCSR() *certv1.CertificateSigningRequest {
	return &certv1.CertificateSigningRequest{
		TypeMeta: metav1.TypeMeta{
			APIVersion: certv1.SchemeGroupVersion.String(),
			Kind:       "CertificateSigningRequest",
		},
		ObjectMeta: metav1.ObjectMeta{Name: "node-csr-123"},
		Spec: certv1.CertificateSigningRequestSpec{
			Request: []byte(`-----BEGIN CERTIFICATE REQUEST-----
MIIBADCBqAIBADBGMRUwEwYDVQQKEwxzeXN0ZW06bm9kZXMxLTArBgNVBAMTJHN5
c3RlbTpub2RlOmdrZS1hbS1nY3AtY2FzdC01ZGM0ZjRlYzBZMBMGByqGSM49AgEG
CCqGSM49AwEHA0IABF/9p5y4t09Y6yAlhF0OthexpL0CEyNHVnVmmbB4jridyJzW
vrcLKbFat0qvJftODQhEA/lqByJepB4YGqQGhregADAKBggqhkjOPQQDAgNHADBE
AiAHVYZXHxxspoV0hcfn2Pdsl89fIPCOFy/K1PqSUR6QNAIgYdt51ZbQt9rgM2BD
39zKjbxU1t82BlrW9/NrmaadNHQ=
-----END CERTIFICATE REQUEST-----`),
			SignerName: certv1.KubeAPIServerClientKubeletSignerName,
			Usages:     []certv1.KeyUsage{certv1.UsageClientAuth},
			Username:   "kubelet-bootstrap",
		},
		// Status: certv1.CertificateSigningRequestStatus{},.
	}
}
