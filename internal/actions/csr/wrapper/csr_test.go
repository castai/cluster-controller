package wrapper_test

import (
	"context"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"testing"
	"time"

	"github.com/samber/lo"
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
	for _, testcase := range []struct {
		name   string
		csrObj runtime.Object
		err    error
	}{
		{
			name: "newCSRFacade() nil arguments",
			err:  wrapper.ErrMalformedCSR,
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
			err: wrapper.ErrMalformedCSR,
		},
		{
			name: "newCSRFacade() V1 spec.Request=nil",
			csrObj: modifyValidV1(t, func(v1 *certv1.CertificateSigningRequest) *certv1.CertificateSigningRequest {
				v1.Spec.Request = nil
				return v1
			}),
			err: wrapper.ErrMalformedCSR,
		},
		{
			name: "newCSRFacade() V1 invalid spec.Request PEM encoding",
			csrObj: modifyValidV1(t, func(v1 *certv1.CertificateSigningRequest) *certv1.CertificateSigningRequest {
				v1.Spec.Request = []byte("invalid certificate request")
				return v1
			}),
			err: wrapper.ErrMalformedCSR,
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
			err: wrapper.ErrMalformedCSR,
		},
		{
			name: "newCSRFacade() V1 spec.Usages=nil",
			csrObj: modifyValidV1(t, func(v1 *certv1.CertificateSigningRequest) *certv1.CertificateSigningRequest {
				v1.Spec.Usages = nil
				return v1
			}),
			err: wrapper.ErrMalformedCSR,
		},
		{
			name: "newCSRFacade() V1 spec.SignerName=\"\"",
			csrObj: modifyValidV1(t, func(v1 *certv1.CertificateSigningRequest) *certv1.CertificateSigningRequest {
				v1.Spec.SignerName = ""
				return v1
			}),
			err: wrapper.ErrMalformedCSR,
		},
		{
			name: "newCSRFacade() V1 spec.Username=\"\"",
			csrObj: modifyValidV1(t, func(v1 *certv1.CertificateSigningRequest) *certv1.CertificateSigningRequest {
				v1.Spec.Username = ""
				return v1
			}),
			err: wrapper.ErrMalformedCSR,
		},
		{
			name: "newCSRFacade() V1Beta1 meta.Name=\"\"",
			csrObj: modifyValidV1Beta1(t, func(v1beta1 *certv1beta1.CertificateSigningRequest) *certv1beta1.CertificateSigningRequest {
				v1beta1.Name = ""
				return v1beta1
			}),
			err: wrapper.ErrMalformedCSR,
		},
		{
			name: "newCSRFacade() V1Beta1 spec.Request=nil",
			csrObj: modifyValidV1Beta1(t, func(v1beta1 *certv1beta1.CertificateSigningRequest) *certv1beta1.CertificateSigningRequest {
				v1beta1.Spec.Request = nil
				return v1beta1
			}),
			err: wrapper.ErrMalformedCSR,
		},
		{
			name: "newCSRFacade() V1Beta1 invalid spec.Request",
			csrObj: modifyValidV1Beta1(t, func(v1beta1 *certv1beta1.CertificateSigningRequest) *certv1beta1.CertificateSigningRequest {
				v1beta1.Spec.Request = []byte("invalid certificate request")
				return v1beta1
			}),
			err: wrapper.ErrMalformedCSR,
		},
		{
			name: "newCSRFacade() V1Beta1 spec.Usages=nil",
			csrObj: modifyValidV1Beta1(t, func(v1beta1 *certv1beta1.CertificateSigningRequest) *certv1beta1.CertificateSigningRequest {
				v1beta1.Spec.Usages = nil
				return v1beta1
			}),
			err: wrapper.ErrMalformedCSR,
		},
		{
			name: "newCSRFacade() V1Beta1 spec.SignerName=nil",
			csrObj: modifyValidV1Beta1(t, func(v1beta1 *certv1beta1.CertificateSigningRequest) *certv1beta1.CertificateSigningRequest {
				v1beta1.Spec.SignerName = nil
				return v1beta1
			}),
			err: wrapper.ErrMalformedCSR,
		},
		{
			name: "newCSRFacade() V1Beta1 spec.SignerName=\"\"",
			csrObj: modifyValidV1Beta1(t, func(v1beta1 *certv1beta1.CertificateSigningRequest) *certv1beta1.CertificateSigningRequest {
				v1beta1.Spec.SignerName = lo.ToPtr("")
				return v1beta1
			}),
			err: wrapper.ErrMalformedCSR,
		},
		{
			name: "newCSRFacade() V1Beta1 spec.Username=\"\"",
			csrObj: modifyValidV1Beta1(t, func(v1beta1 *certv1beta1.CertificateSigningRequest) *certv1beta1.CertificateSigningRequest {
				v1beta1.Spec.Username = ""
				return v1beta1
			}),
			err: wrapper.ErrMalformedCSR,
		},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			_, err := wrapper.NewCSR(fake.NewClientset(), testcase.csrObj)
			if (testcase.err == nil) != (err == nil) || !errors.Is(err, testcase.err) {
				t.Fatalf("want: %v, got: %v", testcase.err, err)
			}
		})
	}
}

func TestCSR_Approved(t *testing.T) {
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
			if v != testcase.result {
				t.Fatalf("approved() want: %v, got: %v", testcase.result, v)
			}
		})
	}
}

func TestCSR_CreatedAt(t *testing.T) {
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
			if v != testcase.result {
				t.Fatalf("CreatedAt() want: %v, got: %v", testcase.result, v)
			}
		})
	}
}

func TestCSR_Name(t *testing.T) {
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
			if err != nil {
				t.Fatalf("failed to create CSR: %v", err)
			}
			if csr.Name() != testcase.result {
				t.Fatalf("Name() want: %v, got: %v", testcase.result, csr.Name())
			}
		})
	}
}

func TestCSR_RequestingUser(t *testing.T) {
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
			if err != nil {
				t.Fatalf("failed to create CSR: %v", err)
			}
			if csr.RequestingUser() != testcase.result {
				t.Fatalf("RequestingUser() want: %v, got: %v", testcase.result, csr.RequestingUser())
			}
		})
	}
}

func TestCSR_SignerName(t *testing.T) {
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
			if err != nil {
				t.Fatalf("failed to create CSR: %v", err)
			}
			if csr.SignerName() != testcase.result {
				t.Fatalf("SignerName() want: %v, got: %v", testcase.result, csr.SignerName())
			}
		})
	}
}

func TestCSR_Usages(t *testing.T) {
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
			if err != nil {
				t.Fatalf("failed to create CSR: %v", err)
			}
			if len(csr.Usages()) != len(testcase.result) {
				t.Fatalf("Usages() length want: %v, got: %v", len(testcase.result), len(csr.Usages()))
			}
			for _, usage := range csr.Usages() {
				if !lo.Contains(testcase.result, usage) {
					t.Fatalf("Usages() contains unexpected: %v", usage)
				}
			}
		})
	}
}

func TestCSR_Groups(t *testing.T) {
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
			if err != nil {
				t.Fatalf("failed to create CSR: %v", err)
			}
			if len(csr.Groups()) != len(testcase.result) {
				t.Fatalf("Groups() length want: %v, got: %v", len(testcase.result), len(csr.Groups()))
			}
			for _, group := range csr.Groups() {
				if !lo.Contains(testcase.result, group) {
					t.Fatalf("Groups() contains unexpected: %v", group)
				}
			}
		})
	}
}

func TestCSR_ParsedCertificateRequest(t *testing.T) {
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
			if err != nil {
				t.Fatalf("failed to create CSR: %v", err)
			}
			got := csr.ParsedCertificateRequest()
			if got == nil {
				t.Fatalf("ParsedCertificateRequest() is nil")
			}
			gotEncoded := pem.EncodeToMemory(&pem.Block{
				Type:  "CERTIFICATE REQUEST",
				Bytes: got.Raw,
			})
			if string(wantEncoded) != string(gotEncoded) {
				t.Fatalf("ParsedCertificateRequest() want: %v, got: %v", string(wantEncoded), string(gotEncoded))
			}
		})
	}
}

func TestCSR_Approve(t *testing.T) {
	for _, testcase := range []struct {
		name   string
		csrObj runtime.Object
		err    error
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
			if err != nil {
				t.Fatalf("failed to create CSR: %v", err)
			}
			err = csr.Approve(context.Background(), "test message")
			if (testcase.err == nil) != (err == nil) || !errors.Is(err, testcase.err) {
				t.Fatalf("Approve() want: %v, got: %v", testcase.err, err)
			}
			if testcase.err == nil && !csr.Approved() {
				t.Fatal("Approved()!=true")
			}
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
			if err != nil {
				t.Fatalf("failed to create CSR: %v", err)
			}
			err = csr.Delete(context.Background())
			if err != nil {
				t.Fatalf("failed to delete CSR: %v", err)
			}
			switch testcase.csrObj.GetObjectKind().GroupVersionKind() {
			case certv1.SchemeGroupVersion.WithKind("CertificateSigningRequest"):
				_, err := clientset.CertificatesV1().CertificateSigningRequests().Get(context.Background(), csr.Name(), metav1.GetOptions{})
				if !k8serrors.IsNotFound(err) {
					t.Fatalf("expected CSR to be deleted, but it still exists: %v", err)
				}
			case certv1beta1.SchemeGroupVersion.WithKind("CertificateSigningRequest"):
				_, err := clientset.CertificatesV1beta1().CertificateSigningRequests().Get(context.Background(), csr.Name(), metav1.GetOptions{})
				if !k8serrors.IsNotFound(err) {
					t.Fatalf("expected CSR to be deleted, but it still exists: %v", err)
				}
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
					clientset.CertificatesV1().CertificateSigningRequests().Create(context.Background(), testcase.csrObj.(*certv1.CertificateSigningRequest), metav1.CreateOptions{})
				case certv1beta1.SchemeGroupVersion.WithKind("CertificateSigningRequest"):
					clientset.CertificatesV1beta1().CertificateSigningRequests().Create(context.Background(), testcase.csrObj.(*certv1beta1.CertificateSigningRequest), metav1.CreateOptions{})
				}
			}
			csr, err := wrapper.NewCSR(clientset, testcase.csrObj)
			if err != nil {
				t.Fatalf("failed to create CSR: %v", err)
			}
			err = csr.CreateOrRefresh(context.Background())
			if err != nil {
				t.Fatalf("failed to createOrRefresh CSR: %v", err)
			}
			switch testcase.csrObj.GetObjectKind().GroupVersionKind() {
			case certv1.SchemeGroupVersion.WithKind("CertificateSigningRequest"):
				_, err := clientset.CertificatesV1().CertificateSigningRequests().Get(context.Background(), csr.Name(), metav1.GetOptions{})
				if err != nil {
					t.Fatalf("failed to get CSR: %v", err)
				}
			case certv1beta1.SchemeGroupVersion.WithKind("CertificateSigningRequest"):
				_, err := clientset.CertificatesV1beta1().CertificateSigningRequests().Get(context.Background(), csr.Name(), metav1.GetOptions{})
				if err != nil {
					t.Fatalf("failed to get CSR: %v", err)
				}
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
	if err != nil {
		t.Fatalf("failed to create CSR: %v", err)
	}
	return result
}

func withConditionsV1Beta1(t *testing.T, clientset kubernetes.Interface, conditions []certv1beta1.CertificateSigningRequestCondition) *wrapper.CSR {
	t.Helper()
	result, err := wrapper.NewCSR(clientset, modifyValidV1Beta1(t, func(v1beta1 *certv1beta1.CertificateSigningRequest) *certv1beta1.CertificateSigningRequest {
		v1beta1.Status.Conditions = conditions
		return v1beta1
	}))
	if err != nil {
		t.Fatalf("failed to create CSR: %v", err)
	}
	return result
}

func v1WithCreationTimestamp(t *testing.T, clientset kubernetes.Interface, creationTime time.Time) *wrapper.CSR {
	t.Helper()
	result, err := wrapper.NewCSR(clientset, modifyValidV1(t, func(v1 *certv1.CertificateSigningRequest) *certv1.CertificateSigningRequest {
		v1.ObjectMeta.CreationTimestamp = metav1.NewTime(creationTime)
		return v1
	}))
	if err != nil {
		t.Fatalf("failed to create CSR: %v", err)
	}
	return result
}

func v1beta1WithCreationTimestamp(t *testing.T, clientset kubernetes.Interface, creationTime time.Time) *wrapper.CSR {
	t.Helper()
	result, err := wrapper.NewCSR(clientset, modifyValidV1Beta1(t, func(v1beta1 *certv1beta1.CertificateSigningRequest) *certv1beta1.CertificateSigningRequest {
		v1beta1.ObjectMeta.CreationTimestamp = metav1.NewTime(creationTime)
		return v1beta1
	}))
	if err != nil {
		t.Fatalf("failed to create CSR: %v", err)
	}
	return result
}
