package facade_test

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"log"
	"testing"
	"time"

	"github.com/samber/lo"
	certv1 "k8s.io/api/certificates/v1"
	certv1beta1 "k8s.io/api/certificates/v1beta1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/castai/cluster-controller/internal/actions/csr/common"
	"github.com/castai/cluster-controller/internal/actions/csr/internal/facade"
)

func TestNewCSR(t *testing.T) {
	for _, testcase := range []struct {
		name    string
		v1      *certv1.CertificateSigningRequest
		v1beta1 *certv1beta1.CertificateSigningRequest
		err     error
	}{
		{
			name: "newCSRFacade() nil arguments",
			err:  common.ErrMalformedCSR,
		},
		{
			name: "newCSRFacade() valid V1",
			v1:   modifyValidV1(t, nil),
		},
		{
			name:    "newCSRFacade() valid V1Beta1",
			v1beta1: modifyValidV1Beta1(t, nil),
		},
		{
			name:    "newCSRFacade() both V1 and V1Beta1 not allowed",
			v1:      modifyValidV1(t, nil),
			v1beta1: modifyValidV1Beta1(t, nil),
			err:     common.ErrMalformedCSR,
		},
		{
			name: "newCSRFacade() V1 meta.Name=\"\"",
			v1: modifyValidV1(t, func(v1 *certv1.CertificateSigningRequest) *certv1.CertificateSigningRequest {
				v1.Name = ""
				return v1
			}),
			err: common.ErrMalformedCSR,
		},
		{
			name: "newCSRFacade() V1 spec.Request=nil",
			v1: modifyValidV1(t, func(v1 *certv1.CertificateSigningRequest) *certv1.CertificateSigningRequest {
				v1.Spec.Request = nil
				return v1
			}),
			err: common.ErrMalformedCSR,
		},
		{
			name: "newCSRFacade() V1 invalid spec.Request PEM encoding",
			v1: modifyValidV1(t, func(v1 *certv1.CertificateSigningRequest) *certv1.CertificateSigningRequest {
				v1.Spec.Request = []byte("invalid certificate request")
				return v1
			}),
			err: common.ErrMalformedCSR,
		},
		{
			name: "newCSRFacade() V1 invalid spec.Request x509 encoding",
			v1: modifyValidV1(t, func(v1 *certv1.CertificateSigningRequest) *certv1.CertificateSigningRequest {
				v1.Spec.Request = pem.EncodeToMemory(&pem.Block{
					Type:  "CERTIFICATE REQUEST",
					Bytes: []byte("invalid certificate request"),
				})
				return v1
			}),
			err: common.ErrMalformedCSR,
		},
		{
			name: "newCSRFacade() V1 spec.Usages=nil",
			v1: modifyValidV1(t, func(v1 *certv1.CertificateSigningRequest) *certv1.CertificateSigningRequest {
				v1.Spec.Usages = nil
				return v1
			}),
			err: common.ErrMalformedCSR,
		},
		{
			name: "newCSRFacade() V1 spec.SignerName=\"\"",
			v1: modifyValidV1(t, func(v1 *certv1.CertificateSigningRequest) *certv1.CertificateSigningRequest {
				v1.Spec.SignerName = ""
				return v1
			}),
			err: common.ErrMalformedCSR,
		},
		{
			name: "newCSRFacade() V1 spec.Username=\"\"",
			v1: modifyValidV1(t, func(v1 *certv1.CertificateSigningRequest) *certv1.CertificateSigningRequest {
				v1.Spec.Username = ""
				return v1
			}),
			err: common.ErrMalformedCSR,
		},
		{
			name: "newCSRFacade() V1Beta1 meta.Name=\"\"",
			v1beta1: modifyValidV1Beta1(t, func(v1beta1 *certv1beta1.CertificateSigningRequest) *certv1beta1.CertificateSigningRequest {
				v1beta1.Name = ""
				return v1beta1
			}),
			err: common.ErrMalformedCSR,
		},
		{
			name: "newCSRFacade() V1Beta1 spec.Request=nil",
			v1beta1: modifyValidV1Beta1(t, func(v1beta1 *certv1beta1.CertificateSigningRequest) *certv1beta1.CertificateSigningRequest {
				v1beta1.Spec.Request = nil
				return v1beta1
			}),
			err: common.ErrMalformedCSR,
		},
		{
			name: "newCSRFacade() V1Beta1 invalid spec.Request",
			v1beta1: modifyValidV1Beta1(t, func(v1beta1 *certv1beta1.CertificateSigningRequest) *certv1beta1.CertificateSigningRequest {
				v1beta1.Spec.Request = []byte("invalid certificate request")
				return v1beta1
			}),
			err: common.ErrMalformedCSR,
		},
		{
			name: "newCSRFacade() V1Beta1 spec.Usages=nil",
			v1beta1: modifyValidV1Beta1(t, func(v1beta1 *certv1beta1.CertificateSigningRequest) *certv1beta1.CertificateSigningRequest {
				v1beta1.Spec.Usages = nil
				return v1beta1
			}),
			err: common.ErrMalformedCSR,
		},
		{
			name: "newCSRFacade() V1Beta1 spec.SignerName=nil",
			v1beta1: modifyValidV1Beta1(t, func(v1beta1 *certv1beta1.CertificateSigningRequest) *certv1beta1.CertificateSigningRequest {
				v1beta1.Spec.SignerName = nil
				return v1beta1
			}),
			err: common.ErrMalformedCSR,
		},
		{
			name: "newCSRFacade() V1Beta1 spec.SignerName=\"\"",
			v1beta1: modifyValidV1Beta1(t, func(v1beta1 *certv1beta1.CertificateSigningRequest) *certv1beta1.CertificateSigningRequest {
				v1beta1.Spec.SignerName = lo.ToPtr("")
				return v1beta1
			}),
			err: common.ErrMalformedCSR,
		},
		{
			name: "newCSRFacade() V1Beta1 spec.Username=\"\"",
			v1beta1: modifyValidV1Beta1(t, func(v1beta1 *certv1beta1.CertificateSigningRequest) *certv1beta1.CertificateSigningRequest {
				v1beta1.Spec.Username = ""
				return v1beta1
			}),
			err: common.ErrMalformedCSR,
		},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			_, err := facade.NewCSR(testcase.v1, testcase.v1beta1)
			if (testcase.err == nil) != (err == nil) || !errors.Is(err, testcase.err) {
				t.Fatalf("want: %v, got: %v", testcase.err, err)
			}
		})
	}
}

func modifyValidV1(t *testing.T, modify func(*certv1.CertificateSigningRequest) *certv1.CertificateSigningRequest) *certv1.CertificateSigningRequest {
	t.Helper()
	result := &certv1.CertificateSigningRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-csr",
		},
		Spec: certv1.CertificateSigningRequestSpec{
			Request:    newEncodedCertificateRequest(t),
			SignerName: "test-signer",
			Username:   "test-user",
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
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-csr",
		},
		Spec: certv1beta1.CertificateSigningRequestSpec{
			Request:    newEncodedCertificateRequest(t),
			SignerName: lo.ToPtr("test-signer"),
			Username:   "test-user",
			Usages:     []certv1beta1.KeyUsage{certv1beta1.UsageClientAuth},
		},
	}
	if modify != nil {
		result = modify(result)
	}
	return result
}

func newEncodedCertificateRequest(t *testing.T) []byte {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate private key: %v", err)
	}

	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName: "test-common-name",
		},
	}, privateKey)
	if err != nil {
		log.Fatalf("CreateCertificateRequest: %v", err)
	}

	return pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE REQUEST",
		Bytes: csrDER,
	})
}

func TestCSR_Approved(t *testing.T) {
	for _, testcase := range []struct {
		name   string
		obj    *facade.CSR
		result bool
	}{
		{
			name: "approved() V1 true",
			obj: withConditionsV1(t, []certv1.CertificateSigningRequestCondition{{
				Type:   certv1.CertificateApproved,
				Status: v1.ConditionTrue,
			}}),
			result: true,
		},
		{
			name: "approved() V1 with denied",
			obj: withConditionsV1(t, []certv1.CertificateSigningRequestCondition{{
				Type:   certv1.CertificateDenied,
				Status: v1.ConditionTrue,
			}}),
		},
		{
			name: "approved() V1 no condition",
			obj:  withConditionsV1(t, nil),
		},
		{
			name: "approved() V1Beta1 true",
			obj: withConditionsV1Beta1(t, []certv1beta1.CertificateSigningRequestCondition{{
				Type:   certv1beta1.CertificateApproved,
				Status: v1.ConditionTrue,
			}}),
			result: true,
		},
		{
			name: "approved() V1Beta1 with denied",
			obj: withConditionsV1(t, []certv1.CertificateSigningRequestCondition{{
				Type:   certv1.CertificateDenied,
				Status: v1.ConditionTrue,
			}}),
		},
		{
			name: "approved() V1Beta1 false",
			obj:  withConditionsV1(t, nil),
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

func withConditionsV1(t *testing.T, conditions []certv1.CertificateSigningRequestCondition) *facade.CSR {
	t.Helper()
	result, err := facade.NewCSR(modifyValidV1(t, func(v1 *certv1.CertificateSigningRequest) *certv1.CertificateSigningRequest {
		v1.Status.Conditions = conditions
		return v1
	}), nil)
	if err != nil {
		t.Fatalf("failed to create CSR: %v", err)
	}
	return result
}

func withConditionsV1Beta1(t *testing.T, conditions []certv1beta1.CertificateSigningRequestCondition) *facade.CSR {
	t.Helper()
	result, err := facade.NewCSR(nil, modifyValidV1Beta1(t, func(v1beta1 *certv1beta1.CertificateSigningRequest) *certv1beta1.CertificateSigningRequest {
		v1beta1.Status.Conditions = conditions
		return v1beta1
	}))
	if err != nil {
		t.Fatalf("failed to create CSR: %v", err)
	}
	return result
}

func TestCSR_Outdated(t *testing.T) {
	for _, testcase := range []struct {
		name   string
		obj    *facade.CSR
		result bool
	}{
		{
			name:   "outdated() V1 true",
			obj:    v1WithCreationTimestamp(t, time.Now().Add(-common.OutdatedDuration)),
			result: true,
		},
		{
			name:   "outdated() V1Beta1 true",
			obj:    v1Beta1WithCreationTimestamp(t, time.Now().Add(-common.OutdatedDuration)),
			result: true,
		},
		{
			name: "outdated() V1 false",
			obj:  v1WithCreationTimestamp(t, time.Now().Add(time.Second-common.OutdatedDuration)),
		},
		{
			name: "outdated() V1Beta1 false",
			obj:  v1Beta1WithCreationTimestamp(t, time.Now().Add(time.Second-common.OutdatedDuration)),
		},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			v := testcase.obj.Outdated()
			if v != testcase.result {
				t.Fatalf("outdated() want: %v, got: %v", testcase.result, v)
			}
		})
	}
}

func v1WithCreationTimestamp(t *testing.T, creationTime time.Time) *facade.CSR {
	t.Helper()
	result, err := facade.NewCSR(modifyValidV1(t, func(v1 *certv1.CertificateSigningRequest) *certv1.CertificateSigningRequest {
		v1.ObjectMeta.CreationTimestamp = metav1.NewTime(creationTime)
		return v1
	}), nil)
	if err != nil {
		t.Fatalf("failed to create CSR: %v", err)
	}
	return result
}

func v1Beta1WithCreationTimestamp(t *testing.T, creationTime time.Time) *facade.CSR {
	t.Helper()
	result, err := facade.NewCSR(nil, modifyValidV1Beta1(t, func(v1beta1 *certv1beta1.CertificateSigningRequest) *certv1beta1.CertificateSigningRequest {
		v1beta1.ObjectMeta.CreationTimestamp = metav1.NewTime(creationTime)
		return v1beta1
	}))
	if err != nil {
		t.Fatalf("failed to create CSR: %v", err)
	}
	return result
}

func TestCSR_Name(t *testing.T) {
	for _, testcase := range []struct {
		name    string
		v1      *certv1.CertificateSigningRequest
		v1beta1 *certv1beta1.CertificateSigningRequest
		result  string
	}{
		{
			name: "name() V1",
			v1: modifyValidV1(t, func(v1 *certv1.CertificateSigningRequest) *certv1.CertificateSigningRequest {
				v1.Name = "test valid v1 name"
				return v1
			}),
			result: "test valid v1 name",
		},
		{
			name: "name() V1Beta1",
			v1beta1: modifyValidV1Beta1(t, func(v1beta1 *certv1beta1.CertificateSigningRequest) *certv1beta1.CertificateSigningRequest {
				v1beta1.Name = "test valid v1beta1 name"
				return v1beta1
			}),
			result: "test valid v1beta1 name",
		},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			csr, err := facade.NewCSR(testcase.v1, testcase.v1beta1)
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
		name    string
		v1      *certv1.CertificateSigningRequest
		v1beta1 *certv1beta1.CertificateSigningRequest
		result  string
	}{
		{
			name: "requestingUser() V1",
			v1: modifyValidV1(t, func(v1 *certv1.CertificateSigningRequest) *certv1.CertificateSigningRequest {
				v1.Spec.Username = "test valid v1 username"
				return v1
			}),
			result: "test valid v1 username",
		},
		{
			name: "requestingUser() V1Beta1",
			v1beta1: modifyValidV1Beta1(t, func(v1beta1 *certv1beta1.CertificateSigningRequest) *certv1beta1.CertificateSigningRequest {
				v1beta1.Spec.Username = "test valid v1beta1 username"
				return v1beta1
			}),
			result: "test valid v1beta1 username",
		},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			csr, err := facade.NewCSR(testcase.v1, testcase.v1beta1)
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
		name    string
		v1      *certv1.CertificateSigningRequest
		v1beta1 *certv1beta1.CertificateSigningRequest
		result  string
	}{
		{
			name: "signerName() V1",
			v1: modifyValidV1(t, func(v1 *certv1.CertificateSigningRequest) *certv1.CertificateSigningRequest {
				v1.Spec.SignerName = "test valid v1 signer name"
				return v1
			}),
			result: "test valid v1 signer name",
		},
		{
			name: "signerName() V1Beta1",
			v1beta1: modifyValidV1Beta1(t, func(v1beta1 *certv1beta1.CertificateSigningRequest) *certv1beta1.CertificateSigningRequest {
				v1beta1.Spec.SignerName = lo.ToPtr("test valid v1beta1 signer name")
				return v1beta1
			}),
			result: "test valid v1beta1 signer name",
		},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			csr, err := facade.NewCSR(testcase.v1, testcase.v1beta1)
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
		name    string
		v1      *certv1.CertificateSigningRequest
		v1beta1 *certv1beta1.CertificateSigningRequest
		result  []string
	}{
		{
			name: "usages() V1",
			v1: modifyValidV1(t, func(v1 *certv1.CertificateSigningRequest) *certv1.CertificateSigningRequest {
				v1.Spec.Usages = []certv1.KeyUsage{certv1.UsageClientAuth}
				return v1
			}),
			result: []string{"client auth"},
		},
		{
			name: "usages() V1Beta1",
			v1beta1: modifyValidV1Beta1(t, func(v1beta1 *certv1beta1.CertificateSigningRequest) *certv1beta1.CertificateSigningRequest {
				v1beta1.Spec.Usages = []certv1beta1.KeyUsage{certv1beta1.UsageClientAuth}
				return v1beta1
			}),
			result: []string{"client auth"},
		},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			csr, err := facade.NewCSR(testcase.v1, testcase.v1beta1)
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

func TestCSR_ParsedCertificateRequest(t *testing.T) {
	wantEncoded := newEncodedCertificateRequest(t)
	for _, testcase := range []struct {
		name    string
		v1      *certv1.CertificateSigningRequest
		v1beta1 *certv1beta1.CertificateSigningRequest
	}{
		{
			name: "parsedCertificateRequest() V1",
			v1: modifyValidV1(t, func(v1 *certv1.CertificateSigningRequest) *certv1.CertificateSigningRequest {
				v1.Spec.Request = wantEncoded
				return v1
			}),
		},
		{
			name: "parsedCertificateRequest() V1Beta1",
			v1beta1: modifyValidV1Beta1(t, func(v1beta1 *certv1beta1.CertificateSigningRequest) *certv1beta1.CertificateSigningRequest {
				v1beta1.Spec.Request = wantEncoded
				return v1beta1
			}),
		},
	} {
		t.Run(testcase.name, func(t *testing.T) {
			csr, err := facade.NewCSR(testcase.v1, testcase.v1beta1)
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
