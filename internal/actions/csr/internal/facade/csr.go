package facade

import (
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"time"

	certv1 "k8s.io/api/certificates/v1"
	certv1beta1 "k8s.io/api/certificates/v1beta1"
	v1 "k8s.io/api/core/v1"

	"github.com/castai/cluster-controller/internal/actions/csr/common"
)

// CSR is plain data holder structure that wraps v1 and v1beta1
// for convenient reading. The one and only reason for this type is because there are
// 2 versions of CertificateSigningRequest that need to be supported and always comparing version
// is not convenient.
// Note for future: no business logic should be added to this type, only getters.
type CSR struct {
	v1      *certv1.CertificateSigningRequest
	v1beta1 *certv1beta1.CertificateSigningRequest

	parsed *x509.CertificateRequest
}

// NewCSR validates and creates new certificateRequestFacade.
func NewCSR(v1 *certv1.CertificateSigningRequest, v1b1 *certv1beta1.CertificateSigningRequest) (*CSR, error) {
	if v1 == nil && v1b1 == nil {
		return nil, fmt.Errorf("either v1 or v1beta1 CertificateSigningRequests expected but got none: %w", common.ErrMalformedCSR)
	}
	if v1 != nil && v1b1 != nil {
		return nil, fmt.Errorf("either v1 or v1beta1 CertificateSigningRequests expected but got both: %w", common.ErrMalformedCSR)
	}
	var parsed *x509.CertificateRequest
	var err error
	if v1 != nil {
		err = validateV1(v1)
		if err != nil {
			return nil, fmt.Errorf("v1 csr invalid: %w", err)
		}
		parsed, err = parseCertificateRequest(v1.Spec.Request)
		if err != nil {
			return nil, err
		}
	}
	if v1b1 != nil {
		err = validateV1Beta1(v1b1)
		if err != nil {
			return nil, fmt.Errorf("v1beta1 csr invalid: %w", err)
		}
		parsed, err = parseCertificateRequest(v1b1.Spec.Request)
		if err != nil {
			return nil, fmt.Errorf("v1beta1 csr invalid: %s: %w", err.Error(), common.ErrMalformedCSR)
		}
	}
	return &CSR{v1, v1b1, parsed}, nil
}

func parseCertificateRequest(raw []byte) (*x509.CertificateRequest, error) {
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("decode CSR PEM block: %w", common.ErrMalformedCSR)
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse CSR: %s: %w", err.Error(), common.ErrMalformedCSR)
	}
	return csr, nil
}

func validateV1(v1 *certv1.CertificateSigningRequest) error {
	if v1.Name == "" {
		return fmt.Errorf("v1 CertificateSigningRequest meta.Name is empty: %w", common.ErrMalformedCSR)
	}
	if v1.Spec.Request == nil {
		return fmt.Errorf("v1 CertificateSigningRequest spec.Request is nil: %w", common.ErrMalformedCSR)
	}
	if v1.Spec.SignerName == "" {
		return fmt.Errorf("v1 CertificateSigningRequest spec.SignerName is empty: %w", common.ErrMalformedCSR)
	}
	if v1.Spec.Username == "" {
		return fmt.Errorf("v1 CertificateSigningRequest spec.Username is empty: %w", common.ErrMalformedCSR)
	}
	if len(v1.Spec.Usages) == 0 {
		return fmt.Errorf("v1 CertificateSigningRequest spec.Usages is empty: %w", common.ErrMalformedCSR)
	}
	return nil
}

func validateV1Beta1(v1b1 *certv1beta1.CertificateSigningRequest) error {
	if v1b1.Name == "" {
		return fmt.Errorf("v1beta1 CertificateSigningRequest meta.Name is empty: %w", common.ErrMalformedCSR)
	}
	if v1b1.Spec.Request == nil {
		return fmt.Errorf("v1beta1 CertificateSigningRequest spec.Request is nil: %w", common.ErrMalformedCSR)
	}
	if v1b1.Spec.SignerName == nil {
		return fmt.Errorf("v1beta1 CertificateSigningRequest spec.SignerName is nil: %w", common.ErrMalformedCSR)
	}
	if *v1b1.Spec.SignerName == "" {
		return fmt.Errorf("v1beta1 CertificateSigningRequest spec.SignerName is empty: %w", common.ErrMalformedCSR)
	}
	if v1b1.Spec.Username == "" {
		return fmt.Errorf("v1beta1 CertificateSigningRequest spec.Username is empty: %w", common.ErrMalformedCSR)
	}
	if len(v1b1.Spec.Usages) == 0 {
		return fmt.Errorf("v1beta1 CertificateSigningRequest spec.Usages is empty: %w", common.ErrMalformedCSR)
	}
	return nil
}

// Approved returns whether the CertificateRequest is approved.
func (f *CSR) Approved() bool {
	if f.v1 != nil {
		for _, condition := range f.v1.Status.Conditions {
			if condition.Type == certv1.CertificateApproved {
				return condition.Status == v1.ConditionTrue
			}
		}
	}
	if f.v1beta1 != nil {
		for _, condition := range f.v1beta1.Status.Conditions {
			if condition.Type == certv1beta1.CertificateApproved {
				return condition.Status == v1.ConditionTrue
			}
		}
	}
	return false
}

// Outdated means that the CertificateRequest is likely not longer valid
// and should be ignored.
func (f *CSR) Outdated() bool {
	if f.v1 != nil {
		return time.Since(f.v1.CreationTimestamp.Time) > common.OutdatedDuration
	}
	return time.Since(f.v1beta1.CreationTimestamp.Time) > common.OutdatedDuration
}

// Name returns the name of the CertificateRequest.
func (f *CSR) Name() string {
	if f.v1 != nil {
		return f.v1.Name
	}
	return f.v1beta1.Name
}

// RequestingUser reads and returns the user that requested the CertificateRequest from v1 or v1beta1.
func (f *CSR) RequestingUser() string {
	if f.v1 != nil {
		return f.v1.Spec.Username
	}
	return f.v1beta1.Spec.Username
}

// SignerName reads and returns the signer name from v1 or v1beta1.
func (f *CSR) SignerName() string {
	if f.v1 != nil {
		return f.v1.Spec.SignerName
	}
	return *f.v1beta1.Spec.SignerName
}

// Usages reads and returns the usages from v1 or v1beta1.
func (f *CSR) Usages() []string {
	var result []string
	if f.v1 != nil {
		for _, usage := range f.v1.Spec.Usages {
			result = append(result, string(usage))
		}
	}
	if f.v1beta1 != nil {
		for _, usage := range f.v1beta1.Spec.Usages {
			result = append(result, string(usage))
		}
	}
	return result
}

// ParsedCertificateRequest returns the CertificateRequest parsed from v1 or v1beta1.
func (f *CSR) ParsedCertificateRequest() *x509.CertificateRequest {
	return f.parsed
}
