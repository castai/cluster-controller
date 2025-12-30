package wrapper_test

import (
	"context"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"
	certv1 "k8s.io/api/certificates/v1"
	certv1beta1 "k8s.io/api/certificates/v1beta1"
	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	csrtest "github.com/castai/cluster-controller/internal/actions/csr/test"
	"github.com/castai/cluster-controller/internal/actions/csr/wrapper"
)

func TestNewCSR(t *testing.T) {
	t.Parallel()
	for _, testcase := range []struct {
		name   string
		csrObj any
		notOK  bool
	}{
		{
			name:  "newCSRFacade() nil arguments",
			notOK: true,
		},
		{
			name:   "newCSRFacade() valid V1",
			csrObj: modifyValidV1(t, nil),
		},
		{
			name:   "newCSRFacade() valid V1Beta1",
			csrObj: modifyValidV1Beta1(t, nil),
		},
		{
			name: "newCSRFacade() V1 meta.Name=\"\"",
			csrObj: modifyValidV1(t, func(v1 *certv1.CertificateSigningRequest) *certv1.CertificateSigningRequest {
				v1.Name = ""
				return v1
			}),
			notOK: true,
		},
		{
			name: "newCSRFacade() V1 spec.Request=nil",
			csrObj: modifyValidV1(t, func(v1 *certv1.CertificateSigningRequest) *certv1.CertificateSigningRequest {
				v1.Spec.Request = nil
				return v1
			}),
			notOK: true,
		},
		{
			name: "newCSRFacade() V1 invalid spec.Request PEM encoding",
			csrObj: modifyValidV1(t, func(v1 *certv1.CertificateSigningRequest) *certv1.CertificateSigningRequest {
				v1.Spec.Request = []byte("invalid certificate request")
				return v1
			}),
			notOK: true,
		},
		{
			name: "newCSRFacade() V1 invalid spec.Request x509 encoding",
			csrObj: modifyValidV1(t, func(v1 *certv1.CertificateSigningRequest) *certv1.CertificateSigningRequest {
				v1.Spec.Request = pem.EncodeToMemory(&pem.Block{
					Type:  "CERTIFICATE REQUEST",
					Bytes: []byte("invalid certificate request"),
				})
				return v1
			}),
			notOK: true,
		},
		{
			name: "newCSRFacade() V1 spec.Usages=nil",
			csrObj: modifyValidV1(t, func(v1 *certv1.CertificateSigningRequest) *certv1.CertificateSigningRequest {
				v1.Spec.Usages = nil
				return v1
			}),
			notOK: true,
		},
		{
			name: "newCSRFacade() V1 spec.SignerName=\"\"",
			csrObj: modifyValidV1(t, func(v1 *certv1.CertificateSigningRequest) *certv1.CertificateSigningRequest {
				v1.Spec.SignerName = ""
				return v1
			}),
			notOK: true,
		},
		{
			name: "newCSRFacade() V1 spec.Username=\"\"",
			csrObj: modifyValidV1(t, func(v1 *certv1.CertificateSigningRequest) *certv1.CertificateSigningRequest {
				v1.Spec.Username = ""
				return v1
			}),
			notOK: true,
		},
		{
			name: "newCSRFacade() V1Beta1 meta.Name=\"\"",
			csrObj: modifyValidV1Beta1(t, func(v1beta1 *certv1beta1.CertificateSigningRequest) *certv1beta1.CertificateSigningRequest {
				v1beta1.Name = ""
				return v1beta1
			}),
			notOK: true,
		},
		{
			name: "newCSRFacade() V1Beta1 spec.Request=nil",
			csrObj: modifyValidV1Beta1(t, func(v1beta1 *certv1beta1.CertificateSigningRequest) *certv1beta1.CertificateSigningRequest {
				v1beta1.Spec.Request = nil
				return v1beta1
			}),
			notOK: true,
		},
		{
			name: "newCSRFacade() V1Beta1 invalid spec.Request",
			csrObj: modifyValidV1Beta1(t, func(v1beta1 *certv1beta1.CertificateSigningRequest) *certv1beta1.CertificateSigningRequest {
				v1beta1.Spec.Request = []byte("invalid certificate request")
				return v1beta1
			}),
			notOK: true,
		},
		{
			name: "newCSRFacade() V1Beta1 spec.Usages=nil",
			csrObj: modifyValidV1Beta1(t, func(v1beta1 *certv1beta1.CertificateSigningRequest) *certv1beta1.CertificateSigningRequest {
				v1beta1.Spec.Usages = nil
				return v1beta1
			}),
			notOK: true,
		},
		{
			name: "newCSRFacade() V1Beta1 spec.SignerName=nil",
			csrObj: modifyValidV1Beta1(t, func(v1beta1 *certv1beta1.CertificateSigningRequest) *certv1beta1.CertificateSigningRequest {
				v1beta1.Spec.SignerName = nil
				return v1beta1
			}),
			notOK: true,
		},
		{
			name: "newCSRFacade() V1Beta1 spec.SignerName=\"\"",
			csrObj: modifyValidV1Beta1(t, func(v1beta1 *certv1beta1.CertificateSigningRequest) *certv1beta1.CertificateSigningRequest {
				v1beta1.Spec.SignerName = lo.ToPtr("")
				return v1beta1
			}),
			notOK: true,
		},
		{
			name: "newCSRFacade() V1Beta1 spec.Username=\"\"",
			csrObj: modifyValidV1Beta1(t, func(v1beta1 *certv1beta1.CertificateSigningRequest) *certv1beta1.CertificateSigningRequest {
				v1beta1.Spec.Username = ""
				return v1beta1
			}),
			notOK: true,
		},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			t.Parallel()
			_, err := wrapper.NewCSR(fake.NewClientset(), testcase.csrObj)
			if testcase.notOK {
				require.Error(t, err, "expected an error but got none")
			} else {
				require.NoError(t, err, "unexpected error")
			}
		})
	}
}

func TestCSR_Approved(t *testing.T) {
	t.Parallel()
	clientset := fake.NewClientset()
	for _, testcase := range []struct {
		name   string
		obj    *wrapper.CSR
		result bool
	}{
		{
			name: "approved() V1 true",
			obj: withConditionsV1(t, clientset, []certv1.CertificateSigningRequestCondition{{
				Type:   certv1.CertificateApproved,
				Status: v1.ConditionTrue,
			}}),
			result: true,
		},
		{
			name: "approved() V1 with denied",
			obj: withConditionsV1(t, clientset, []certv1.CertificateSigningRequestCondition{{
				Type:   certv1.CertificateDenied,
				Status: v1.ConditionTrue,
			}}),
		},
		{
			name: "approved() V1 no condition",
			obj:  withConditionsV1(t, clientset, nil),
		},
		{
			name: "approved() V1Beta1 true",
			obj: withConditionsV1Beta1(t, clientset, []certv1beta1.CertificateSigningRequestCondition{{
				Type:   certv1beta1.CertificateApproved,
				Status: v1.ConditionTrue,
			}}),
			result: true,
		},
		{
			name: "approved() V1Beta1 with denied",
			obj: withConditionsV1(t, clientset, []certv1.CertificateSigningRequestCondition{{
				Type:   certv1.CertificateDenied,
				Status: v1.ConditionTrue,
			}}),
		},
		{
			name: "approved() V1Beta1 false",
			obj:  withConditionsV1(t, clientset, nil),
		},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			v := testcase.obj.Approved()
			require.Equal(t, testcase.result, v, "approved() mismatch")
		})
	}
}

func TestCSR_CreatedAt(t *testing.T) {
	t.Parallel()
	clientset := fake.NewClientset()
	testTime := time.Now().Add(-time.Hour)
	for _, testcase := range []struct {
		name   string
		obj    *wrapper.CSR
		result time.Time
	}{
		{
			name:   "CreatedAt() V1",
			obj:    v1WithCreationTimestamp(t, clientset, testTime),
			result: testTime,
		},
		{
			name:   "CreatedAt() V1Beta1",
			obj:    v1beta1WithCreationTimestamp(t, clientset, testTime),
			result: testTime,
		},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			v := testcase.obj.CreatedAt()
			require.Equal(t, testcase.result, v, "CreatedAt() mismatch")
		})
	}
}

func TestCSR_Name(t *testing.T) {
	t.Parallel()
	for _, testcase := range []struct {
		name   string
		csrObj runtime.Object
		result string
	}{
		{
			name: "name() V1",
			csrObj: modifyValidV1(t, func(v1 *certv1.CertificateSigningRequest) *certv1.CertificateSigningRequest {
				v1.Name = "test valid v1 name"
				return v1
			}),
			result: "test valid v1 name",
		},
		{
			name: "name() V1Beta1",
			csrObj: modifyValidV1Beta1(t, func(v1beta1 *certv1beta1.CertificateSigningRequest) *certv1beta1.CertificateSigningRequest {
				v1beta1.Name = "test valid v1beta1 name"
				return v1beta1
			}),
			result: "test valid v1beta1 name",
		},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			csr, err := wrapper.NewCSR(fake.NewClientset(), testcase.csrObj)
			require.NoError(t, err, "failed to create CSR")
			require.Equal(t, testcase.result, csr.Name(), "Name() mismatch")
		})
	}
}

func TestCSR_RequestingUser(t *testing.T) {
	t.Parallel()
	for _, testcase := range []struct {
		name   string
		csrObj runtime.Object
		result string
	}{
		{
			name: "requestingUser() V1",
			csrObj: modifyValidV1(t, func(v1 *certv1.CertificateSigningRequest) *certv1.CertificateSigningRequest {
				v1.Spec.Username = "test valid v1 username"
				return v1
			}),
			result: "test valid v1 username",
		},
		{
			name: "requestingUser() V1Beta1",
			csrObj: modifyValidV1Beta1(t, func(v1beta1 *certv1beta1.CertificateSigningRequest) *certv1beta1.CertificateSigningRequest {
				v1beta1.Spec.Username = "test valid v1beta1 username"
				return v1beta1
			}),
			result: "test valid v1beta1 username",
		},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			csr, err := wrapper.NewCSR(fake.NewClientset(), testcase.csrObj)
			require.NoError(t, err, "failed to create CSR")
			require.Equal(t, testcase.result, csr.RequestingUser(), "RequestingUser() mismatch")
		})
	}
}

func TestCSR_SignerName(t *testing.T) {
	t.Parallel()
	for _, testcase := range []struct {
		name   string
		csrObj runtime.Object
		result string
	}{
		{
			name: "signerName() V1",
			csrObj: modifyValidV1(t, func(v1 *certv1.CertificateSigningRequest) *certv1.CertificateSigningRequest {
				v1.Spec.SignerName = "test valid v1 signer name"
				return v1
			}),
			result: "test valid v1 signer name",
		},
		{
			name: "signerName() V1Beta1",
			csrObj: modifyValidV1Beta1(t, func(v1beta1 *certv1beta1.CertificateSigningRequest) *certv1beta1.CertificateSigningRequest {
				v1beta1.Spec.SignerName = lo.ToPtr("test valid v1beta1 signer name")
				return v1beta1
			}),
			result: "test valid v1beta1 signer name",
		},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			csr, err := wrapper.NewCSR(fake.NewClientset(), testcase.csrObj)
			require.NoError(t, err, "failed to create CSR")
			require.Equal(t, testcase.result, csr.SignerName(), "SignerName() mismatch")
		})
	}
}

func TestCSR_Usages(t *testing.T) {
	t.Parallel()
	for _, testcase := range []struct {
		name   string
		csrObj runtime.Object
		result []string
	}{
		{
			name: "usages() V1",
			csrObj: modifyValidV1(t, func(v1 *certv1.CertificateSigningRequest) *certv1.CertificateSigningRequest {
				v1.Spec.Usages = []certv1.KeyUsage{certv1.UsageClientAuth}
				return v1
			}),
			result: []string{"client auth"},
		},
		{
			name: "usages() V1Beta1",
			csrObj: modifyValidV1Beta1(t, func(v1beta1 *certv1beta1.CertificateSigningRequest) *certv1beta1.CertificateSigningRequest {
				v1beta1.Spec.Usages = []certv1beta1.KeyUsage{certv1beta1.UsageClientAuth}
				return v1beta1
			}),
			result: []string{"client auth"},
		},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			csr, err := wrapper.NewCSR(fake.NewClientset(), testcase.csrObj)
			require.NoError(t, err, "failed to create CSR")
			require.ElementsMatch(t, testcase.result, csr.Usages(), "Usages() mismatch")
		})
	}
}

func TestCSR_Groups(t *testing.T) {
	t.Parallel()
	for _, testcase := range []struct {
		name   string
		csrObj runtime.Object
		result []string
	}{
		{
			name: "groups() V1",
			csrObj: modifyValidV1(t, func(v1 *certv1.CertificateSigningRequest) *certv1.CertificateSigningRequest {
				v1.Spec.Groups = []string{"test-group"}
				return v1
			}),
			result: []string{"test-group"},
		},
		{
			name: "groups() V1Beta1",
			csrObj: modifyValidV1Beta1(t, func(v1beta1 *certv1beta1.CertificateSigningRequest) *certv1beta1.CertificateSigningRequest {
				v1beta1.Spec.Groups = []string{"test-group"}
				return v1beta1
			}),
			result: []string{"test-group"},
		},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			csr, err := wrapper.NewCSR(fake.NewClientset(), testcase.csrObj)
			require.NoError(t, err, "failed to create CSR")
			require.ElementsMatch(t, testcase.result, csr.Groups(), "Groups() mismatch")
		})
	}
}

func TestCSR_ParsedCertificateRequest(t *testing.T) {
	t.Parallel()
	wantEncoded := csrtest.NewEncodedCertificateRequest(t, &x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName: "test-subject-common-name",
		},
	})
	for _, testcase := range []struct {
		name   string
		csrObj runtime.Object
	}{
		{
			name: "parsedCertificateRequest() V1",
			csrObj: modifyValidV1(t, func(v1 *certv1.CertificateSigningRequest) *certv1.CertificateSigningRequest {
				v1.Spec.Request = wantEncoded
				return v1
			}),
		},
		{
			name: "parsedCertificateRequest() V1Beta1",
			csrObj: modifyValidV1Beta1(t, func(v1beta1 *certv1beta1.CertificateSigningRequest) *certv1beta1.CertificateSigningRequest {
				v1beta1.Spec.Request = wantEncoded
				return v1beta1
			}),
		},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			csr, err := wrapper.NewCSR(fake.NewClientset(), testcase.csrObj)
			require.NoError(t, err, "failed to create CSR")
			got := csr.ParsedCertificateRequest()
			require.NotNil(t, got, "ParsedCertificateRequest() is nil")
			gotEncoded := pem.EncodeToMemory(&pem.Block{
				Type:  "CERTIFICATE REQUEST",
				Bytes: got.Raw,
			})
			require.Equal(t, string(wantEncoded), string(gotEncoded), "ParsedCertificateRequest() mismatch")
		})
	}
}

func TestCSR_Approve(t *testing.T) {
	t.Parallel()
	for _, testcase := range []struct {
		name   string
		csrObj runtime.Object
	}{
		{
			name: "Approve() V1 OK",
			csrObj: modifyValidV1(t, func(v1 *certv1.CertificateSigningRequest) *certv1.CertificateSigningRequest {
				v1.Status.Conditions = nil
				return v1
			}),
		},
		{
			name: "Approve() V1Beta1 OK",
			csrObj: modifyValidV1Beta1(t, func(v1beta1 *certv1beta1.CertificateSigningRequest) *certv1beta1.CertificateSigningRequest {
				v1beta1.Status.Conditions = nil
				return v1beta1
			}),
		},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			clientset := fake.NewClientset(testcase.csrObj)
			csr, err := wrapper.NewCSR(clientset, testcase.csrObj)
			require.NoError(t, err, "failed to create CSR")
			err = csr.Approve(context.Background(), "test message")
			require.NoError(t, err, "unexpected error in Approve()")
			require.True(t, csr.Approved(), "Approved() should return true")
		})
	}
}

func TestCSR_Delete(t *testing.T) {
	t.Parallel()
	for _, testcase := range []struct {
		name   string
		csrObj runtime.Object
	}{
		{
			name:   "delete() V1",
			csrObj: modifyValidV1(t, nil),
		},
		{
			name:   "delete() V1Beta1",
			csrObj: modifyValidV1Beta1(t, nil),
		},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			clientset := fake.NewClientset(testcase.csrObj)
			csr, err := wrapper.NewCSR(clientset, testcase.csrObj)
			require.NoError(t, err, "failed to create CSR")
			err = csr.Delete(context.Background())
			require.NoError(t, err, "failed to delete CSR")
			switch testcase.csrObj.GetObjectKind().GroupVersionKind() {
			case certv1.SchemeGroupVersion.WithKind("CertificateSigningRequest"):
				_, err := clientset.CertificatesV1().CertificateSigningRequests().Get(context.Background(), csr.Name(), metav1.GetOptions{})
				require.True(t, k8serrors.IsNotFound(err), "expected CSR to be deleted")
			case certv1beta1.SchemeGroupVersion.WithKind("CertificateSigningRequest"):
				_, err := clientset.CertificatesV1beta1().CertificateSigningRequests().Get(context.Background(), csr.Name(), metav1.GetOptions{})
				require.True(t, k8serrors.IsNotFound(err), "expected CSR to be deleted")
			}
		})
	}
}

func TestCSR_CreateOrRefresh(t *testing.T) {
	t.Parallel()

	for _, testcase := range []struct {
		absent bool
		name   string
		csrObj runtime.Object
	}{
		{
			name:   "createOrRefresh() V1 when exists",
			csrObj: modifyValidV1(t, nil),
		},
		{
			absent: true,
			name:   "createOrRefresh() V1 when absent",
			csrObj: modifyValidV1(t, nil),
		},
		{
			name:   "createOrRefresh() V1Beta1 when exists",
			csrObj: modifyValidV1Beta1(t, nil),
		},
		{
			absent: true,
			name:   "createOrRefresh() V1Beta1 when absent",
			csrObj: modifyValidV1Beta1(t, nil),
		},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			clientset := fake.NewClientset()
			if !testcase.absent {
				switch testcase.csrObj.GetObjectKind().GroupVersionKind() {
				case certv1.SchemeGroupVersion.WithKind("CertificateSigningRequest"):
					_, err := clientset.CertificatesV1().CertificateSigningRequests().Create(context.Background(), testcase.csrObj.(*certv1.CertificateSigningRequest), metav1.CreateOptions{})
					require.NoError(t, err, "failed to create CSR")
				case certv1beta1.SchemeGroupVersion.WithKind("CertificateSigningRequest"):
					_, err := clientset.CertificatesV1beta1().CertificateSigningRequests().Create(context.Background(), testcase.csrObj.(*certv1beta1.CertificateSigningRequest), metav1.CreateOptions{})
					require.NoError(t, err, "failed to create CSR")
				}
			}
			csr, err := wrapper.NewCSR(clientset, testcase.csrObj)
			require.NoError(t, err, "failed to create CSR")
			err = csr.CreateOrRefresh(context.Background())
			require.NoError(t, err, "failed to createOrRefresh CSR")
			switch testcase.csrObj.GetObjectKind().GroupVersionKind() {
			case certv1.SchemeGroupVersion.WithKind("CertificateSigningRequest"):
				_, err := clientset.CertificatesV1().CertificateSigningRequests().Get(context.Background(), csr.Name(), metav1.GetOptions{})
				require.NoError(t, err, "failed to get CSR")
			case certv1beta1.SchemeGroupVersion.WithKind("CertificateSigningRequest"):
				_, err := clientset.CertificatesV1beta1().CertificateSigningRequests().Get(context.Background(), csr.Name(), metav1.GetOptions{})
				require.NoError(t, err, "failed to get CSR")
			}
		})
	}
}

func modifyValidV1(t *testing.T, modify func(*certv1.CertificateSigningRequest) *certv1.CertificateSigningRequest) *certv1.CertificateSigningRequest {
	t.Helper()

	result := &certv1.CertificateSigningRequest{
		TypeMeta: metav1.TypeMeta{
			APIVersion: certv1.SchemeGroupVersion.String(),
			Kind:       "CertificateSigningRequest",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-csr",
			CreationTimestamp: metav1.Now(),
		},
		Spec: certv1.CertificateSigningRequestSpec{
			Request: csrtest.NewEncodedCertificateRequest(t, &x509.CertificateRequest{
				Subject: pkix.Name{
					CommonName: "test-common-name",
				},
			}),
			SignerName: certv1.KubeAPIServerClientKubeletSignerName,
			Username:   "kubelet-bootstrap",
			Usages:     []certv1.KeyUsage{certv1.UsageClientAuth},
		},
	}
	if modify != nil {
		result = modify(result)
	}
	return result
}

func modifyValidV1Beta1(t *testing.T, modify func(*certv1beta1.CertificateSigningRequest) *certv1beta1.CertificateSigningRequest) *certv1beta1.CertificateSigningRequest {
	t.Helper()
	result := &certv1beta1.CertificateSigningRequest{
		TypeMeta: metav1.TypeMeta{
			APIVersion: certv1beta1.SchemeGroupVersion.String(),
			Kind:       "CertificateSigningRequest",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-csr",
		},
		Spec: certv1beta1.CertificateSigningRequestSpec{
			Request: csrtest.NewEncodedCertificateRequest(t, &x509.CertificateRequest{
				Subject: pkix.Name{
					CommonName: "test-common-name",
				},
			}),
			SignerName: lo.ToPtr(certv1.KubeAPIServerClientKubeletSignerName),
			Username:   "kubelet-bootstrap",
			Usages:     []certv1beta1.KeyUsage{certv1beta1.UsageClientAuth},
		},
	}
	if modify != nil {
		result = modify(result)
	}
	return result
}

func withConditionsV1(t *testing.T, clientset kubernetes.Interface, conditions []certv1.CertificateSigningRequestCondition) *wrapper.CSR {
	t.Helper()
	result, err := wrapper.NewCSR(clientset, modifyValidV1(t, func(v1 *certv1.CertificateSigningRequest) *certv1.CertificateSigningRequest {
		v1.Status.Conditions = conditions
		return v1
	}))
	require.NoError(t, err, "failed to create CSR")
	return result
}

func withConditionsV1Beta1(t *testing.T, clientset kubernetes.Interface, conditions []certv1beta1.CertificateSigningRequestCondition) *wrapper.CSR {
	t.Helper()
	result, err := wrapper.NewCSR(clientset, modifyValidV1Beta1(t, func(v1beta1 *certv1beta1.CertificateSigningRequest) *certv1beta1.CertificateSigningRequest {
		v1beta1.Status.Conditions = conditions
		return v1beta1
	}))
	require.NoError(t, err, "failed to create CSR")
	return result
}

func v1WithCreationTimestamp(t *testing.T, clientset kubernetes.Interface, creationTime time.Time) *wrapper.CSR {
	t.Helper()
	result, err := wrapper.NewCSR(clientset, modifyValidV1(t, func(v1 *certv1.CertificateSigningRequest) *certv1.CertificateSigningRequest {
		v1.CreationTimestamp = metav1.NewTime(creationTime)
		return v1
	}))
	require.NoError(t, err, "failed to create CSR")
	return result
}

func v1beta1WithCreationTimestamp(t *testing.T, clientset kubernetes.Interface, creationTime time.Time) *wrapper.CSR {
	t.Helper()
	result, err := wrapper.NewCSR(clientset, modifyValidV1Beta1(t, func(v1beta1 *certv1beta1.CertificateSigningRequest) *certv1beta1.CertificateSigningRequest {
		v1beta1.CreationTimestamp = metav1.NewTime(creationTime)
		return v1beta1
	}))
	require.NoError(t, err, "failed to create CSR")
	return result
}
