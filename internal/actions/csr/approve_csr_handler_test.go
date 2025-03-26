package csr

import (
	"context"
	"errors"
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
)

func TestApproveCSRHandler(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)

	t.Run("do not process approved v1 csr successfully", func(t *testing.T) {
		r := require.New(t)

		csrRes := getCSR()
		client := fake.NewSimpleClientset(csrRes)
		am := NewApprovalManager(log, client)
		csrRes.Status.Conditions = []certv1.CertificateSigningRequestCondition{
			{
				Type: certv1.CertificateApproved,
			},
		}

		ctx := context.Background()
		err := am.handle(ctx, log, &Certificate{v1: csrRes})
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
		client := fake.NewSimpleClientset(csrRes)
		client.PrependReactor("list", "certificatesigningrequests", fn)

		am := NewApprovalManager(log, client)

		err := am.handle(context.Background(), log, &Certificate{v1: csrRes})
		r.NoError(err)
	})

	t.Run("approve v1beta1 csr successfully", func(t *testing.T) {
		r := require.New(t)

		signer := certv1beta1.KubeAPIServerClientKubeletSignerName
		csrRes := &certv1beta1.CertificateSigningRequest{
			ObjectMeta: metav1.ObjectMeta{Name: "node-csr-123"},
			Spec: certv1beta1.CertificateSigningRequestSpec{
				Request: []byte(`-----BEGIN CERTIFICATE REQUEST-----
	MIIBADCBqAIBADBGMRUwEwYDVQQKEwxzeXN0ZW06bm9kZXMxLTArBgNVBAMTJHN5
	c3RlbTpub2RlOmdrZS1hbS1nY3AtY2FzdC01ZGM0ZjRlYzBZMBMGByqGSM49AgEG
	CCqGSM49AwEHA0IABF/9p5y4t09Y6yAlhF0OthexpL0CEyNHVnVmmbB4jridyJzW
	vrcLKbFat0qvJftODQhEA/lqByJepB4YGqQGhregADAKBggqhkjOPQQDAgNHADBE
	AiAHVYZXHxxspoV0hcfn2Pdsl89fIPCOFy/K1PqSUR6QNAIgYdt51ZbQt9rgM2BD
	39zKjbxU1t82BlrW9/NrmaadNHQ=
	-----END CERTIFICATE REQUEST-----`),
				Username:   "kubelet",
				SignerName: &signer,
			},
		}
		client := fake.NewSimpleClientset(csrRes)
		// Return NotFound for all v1 resources.
		client.PrependReactor("*", "*", func(action ktest.Action) (handled bool, ret runtime.Object, err error) {
			if action.GetResource().Version == "v1" {
				err = apierrors.NewNotFound(schema.GroupResource{}, action.GetResource().String())
				return true, nil, err
			}
			return
		})
		client.PrependReactor("update", "certificatesigningrequests", func(action ktest.Action) (handled bool, ret runtime.Object, err error) {
			approved := csrRes.DeepCopy()
			approved.Status.Conditions = []certv1beta1.CertificateSigningRequestCondition{
				{
					Type:           certv1beta1.CertificateApproved,
					Reason:         ReasonApproved,
					Message:        "approved",
					LastUpdateTime: metav1.Now(),
					Status:         v1.ConditionTrue,
				},
			}
			return true, approved, nil
		})

		am := NewApprovalManager(log, client)
		err := am.handle(context.Background(), log, &Certificate{v1Beta1: csrRes})
		r.NoError(err)
	})

	t.Run("approve v1beta1 csr failed", func(t *testing.T) {
		r := require.New(t)

		signer := certv1beta1.KubeAPIServerClientKubeletSignerName
		csrRes := &certv1beta1.CertificateSigningRequest{
			ObjectMeta: metav1.ObjectMeta{Name: "node-csr-123"},
			Spec: certv1beta1.CertificateSigningRequestSpec{
				Request: []byte(`-----BEGIN CERTIFICATE REQUEST-----
	MIIBADCBqAIBADBGMRUwEwYDVQQKEwxzeXN0ZW06bm9kZXMxLTArBgNVBAMTJHN5
	c3RlbTpub2RlOmdrZS1hbS1nY3AtY2FzdC01ZGM0ZjRlYzBZMBMGByqGSM49AgEG
	CCqGSM49AwEHA0IABF/9p5y4t09Y6yAlhF0OthexpL0CEyNHVnVmmbB4jridyJzW
	vrcLKbFat0qvJftODQhEA/lqByJepB4YGqQGhregADAKBggqhkjOPQQDAgNHADBE
	AiAHVYZXHxxspoV0hcfn2Pdsl89fIPCOFy/K1PqSUR6QNAIgYdt51ZbQt9rgM2BD
	39zKjbxU1t82BlrW9/NrmaadNHQ=
	-----END CERTIFICATE REQUEST-----`),
				Username:   "kubelet",
				SignerName: &signer,
			},
		}
		client := fake.NewSimpleClientset(csrRes)
		// Return NotFound for all v1 resources.
		client.PrependReactor("*", "*", func(action ktest.Action) (handled bool, ret runtime.Object, err error) {
			if action.GetResource().Version == "v1" {
				err = apierrors.NewNotFound(schema.GroupResource{}, action.GetResource().String())
				return true, nil, err
			}
			return
		})
		client.PrependReactor("update", "certificatesigningrequests", func(action ktest.Action) (handled bool, ret runtime.Object, err error) {
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
		err := am.handle(context.Background(), log, &Certificate{v1Beta1: csrRes})
		r.Error(err)
	})
}

func TestApproveCSRExponentialBackoff(t *testing.T) {
	r := require.New(t)
	b := newApproveCSRExponentialBackoff()
	var sum time.Duration
	for i := 0; i < 10; i++ {
		tmp := b.Step()
		sum += tmp
	}
	r.Truef(100 < sum.Seconds(), "actual elapsed seconds %v", sum.Seconds())
}

func getCSR() *certv1.CertificateSigningRequest {
	return &certv1.CertificateSigningRequest{
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
			Usages:     []certv1.KeyUsage{"kubelet"},
			Username:   "kubelet-bootstrap",
		},
		// Status: certv1.CertificateSigningRequestStatus{},.
	}
}
