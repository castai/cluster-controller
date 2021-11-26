package actions

import (
	"context"
	"testing"
	"time"

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

	"github.com/castai/cluster-controller/castai"
)

func TestApproveCSRHandler(t *testing.T) {
	r := require.New(t)
	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)

	t.Run("approve v1 csr successfully", func(t *testing.T) {
		csr := &certv1.CertificateSigningRequest{
			TypeMeta: metav1.TypeMeta{},
			ObjectMeta: metav1.ObjectMeta{
				Name: "node-csr-123",
			},
			Spec: certv1.CertificateSigningRequestSpec{
				Request: []byte(`-----BEGIN CERTIFICATE REQUEST-----
MIIBADCBqAIBADBGMRUwEwYDVQQKEwxzeXN0ZW06bm9kZXMxLTArBgNVBAMTJHN5
c3RlbTpub2RlOmdrZS1hbS1nY3AtY2FzdC01ZGM0ZjRlYzBZMBMGByqGSM49AgEG
CCqGSM49AwEHA0IABF/9p5y4t09Y6yAlhF0OthexpL0CEyNHVnVmmbB4jridyJzW
vrcLKbFat0qvJftODQhEA/lqByJepB4YGqQGhregADAKBggqhkjOPQQDAgNHADBE
AiAHVYZXHxxspoV0hcfn2Pdsl89fIPCOFy/K1PqSUR6QNAIgYdt51ZbQt9rgM2BD
39zKjbxU1t82BlrW9/NrmaadNHQ=
-----END CERTIFICATE REQUEST-----`),
				SignerName:        "kubelet",
				ExpirationSeconds: nil,
				Usages: []certv1.KeyUsage{
					certv1.KeyUsage("kubelet"),
				},
				Username: "kubelet",
				UID:      "",
				Groups:   nil,
				Extra:    nil,
			},
			Status: certv1.CertificateSigningRequestStatus{},
		}
		client := fake.NewSimpleClientset(csr)
		// Return NotFound for all v1beta1 resources.
		client.PrependReactor("*", "*", func(action ktest.Action) (handled bool, ret runtime.Object, err error) {
			if action.GetResource().Version == "v1beta1" {
				err = apierrors.NewNotFound(schema.GroupResource{}, action.GetResource().String())
				return true, nil, err
			}
			return
		})
		client.PrependReactor("update", "certificatesigningrequests", func(action ktest.Action) (handled bool, ret runtime.Object, err error) {
			approved := csr.DeepCopy()
			approved.Status.Conditions = []certv1.CertificateSigningRequestCondition{
				{
					Type:           certv1.CertificateApproved,
					Reason:         "CastaiApprove",
					Message:        "approved",
					LastUpdateTime: metav1.Now(),
					Status:         v1.ConditionTrue,
				},
			}
			return true, approved, nil
		})

		h := &approveCSRHandler{
			log:                    log,
			clientset:              client,
			csrFetchInterval:       1 * time.Millisecond,
			initialCSRFetchTimeout: 10 * time.Millisecond,
		}

		ctx := context.Background()
		err := h.Handle(ctx, &castai.ActionApproveCSR{NodeName: "gke-am-gcp-cast-5dc4f4ec"})
		r.NoError(err)
	})

	t.Run("approve v1beta1 csr successfully", func(t *testing.T) {
		csr := &certv1beta1.CertificateSigningRequest{
			TypeMeta: metav1.TypeMeta{},
			ObjectMeta: metav1.ObjectMeta{
				Name: "node-csr-123",
			},
			Spec: certv1beta1.CertificateSigningRequestSpec{
				Request: []byte(`-----BEGIN CERTIFICATE REQUEST-----
MIIBADCBqAIBADBGMRUwEwYDVQQKEwxzeXN0ZW06bm9kZXMxLTArBgNVBAMTJHN5
c3RlbTpub2RlOmdrZS1hbS1nY3AtY2FzdC01ZGM0ZjRlYzBZMBMGByqGSM49AgEG
CCqGSM49AwEHA0IABF/9p5y4t09Y6yAlhF0OthexpL0CEyNHVnVmmbB4jridyJzW
vrcLKbFat0qvJftODQhEA/lqByJepB4YGqQGhregADAKBggqhkjOPQQDAgNHADBE
AiAHVYZXHxxspoV0hcfn2Pdsl89fIPCOFy/K1PqSUR6QNAIgYdt51ZbQt9rgM2BD
39zKjbxU1t82BlrW9/NrmaadNHQ=
-----END CERTIFICATE REQUEST-----`),
				ExpirationSeconds: nil,
				Username:          "kubelet",
				UID:               "",
				Groups:            nil,
				Extra:             nil,
			},
			Status: certv1beta1.CertificateSigningRequestStatus{},
		}
		client := fake.NewSimpleClientset(csr)
		// Return NotFound for all v1 resources.
		client.PrependReactor("*", "*", func(action ktest.Action) (handled bool, ret runtime.Object, err error) {
			if action.GetResource().Version == "v1" {
				err = apierrors.NewNotFound(schema.GroupResource{}, action.GetResource().String())
				return true, nil, err
			}
			return
		})

		client.PrependReactor("update", "certificatesigningrequests", func(action ktest.Action) (handled bool, ret runtime.Object, err error) {
			approved := csr.DeepCopy()
			approved.Status.Conditions = []certv1beta1.CertificateSigningRequestCondition{
				{
					Type:           certv1beta1.CertificateApproved,
					Reason:         "CastaiApprove",
					Message:        "approved",
					LastUpdateTime: metav1.Now(),
					Status:         v1.ConditionTrue,
				},
			}
			return true, approved, nil
		})

		h := &approveCSRHandler{
			log:                    log,
			clientset:              client,
			csrFetchInterval:       1 * time.Millisecond,
			initialCSRFetchTimeout: 10 * time.Millisecond,
		}

		ctx := context.Background()
		err := h.Handle(ctx, &castai.ActionApproveCSR{NodeName: "gke-am-gcp-cast-5dc4f4ec"})
		r.NoError(err)
	})

	t.Run("return timeout error when no initial csr found for node", func(t *testing.T) {
		client := fake.NewSimpleClientset()
		h := &approveCSRHandler{
			log:                    log,
			clientset:              client,
			csrFetchInterval:       1 * time.Millisecond,
			initialCSRFetchTimeout: 10 * time.Millisecond,
		}

		ctx := context.Background()
		err := h.Handle(ctx, &castai.ActionApproveCSR{NodeName: "node"})
		r.EqualError(err, "getting initial csr: context deadline exceeded")
	})
}
