package wrapper

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"strings"
	"time"

	certv1 "k8s.io/api/certificates/v1"
	certv1beta1 "k8s.io/api/certificates/v1beta1"
	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	certificatesv1 "k8s.io/client-go/kubernetes/typed/certificates/v1"
	certificatesv1beta1 "k8s.io/client-go/kubernetes/typed/certificates/v1beta1"
)

// CSR wraps v1 and v1beta1 for convenient read/write.
// The one and only reason for this type is because there are
// 2 versions of CertificateSigningRequest that need to be supported and always checking version
// is not convenient.
// Note for future: no business logic should be added to this wrapper.
type CSR struct {
	v1      *certv1.CertificateSigningRequest
	v1beta1 *certv1beta1.CertificateSigningRequest

	certificatesV1      certificatesv1.CertificatesV1Interface
	certificatesV1beta1 certificatesv1beta1.CertificatesV1beta1Interface

	parsed *x509.CertificateRequest
}

// NewCSR validates and creates new certificateRequestFacade.
func NewCSR(clientset kubernetes.Interface, csrObj any) (*CSR, error) {
	var (
		v1   *certv1.CertificateSigningRequest
		v1b1 *certv1beta1.CertificateSigningRequest
	)
	if csrObj == nil {
		return nil, fmt.Errorf("either v1 or v1beta1 CertificateSigningRequests expected but got none")
	}
	switch csr := csrObj.(type) {
	case *certv1.CertificateSigningRequest:
		v1 = csr
	case *certv1beta1.CertificateSigningRequest:
		v1b1 = csr
	default:
		return nil, fmt.Errorf("either v1 or v1beta1 CertificateSigningRequests expected but got %T", csrObj)
	}
	var result CSR
	var err error
	if v1 != nil {
		err = validateV1(v1)
		if err != nil {
			return nil, fmt.Errorf("v1 csr invalid: %w", err)
		}
		result.certificatesV1 = clientset.CertificatesV1()
		result.v1 = v1
		result.parsed, err = parseCertificateRequest(v1.Spec.Request)
		if err != nil {
			return nil, err
		}
	}
	if v1b1 != nil {
		err = validateV1Beta1(v1b1)
		if err != nil {
			return nil, fmt.Errorf("v1beta1 csr invalid: %w", err)
		}
		result.certificatesV1beta1 = clientset.CertificatesV1beta1()
		result.v1beta1 = v1b1
		result.parsed, err = parseCertificateRequest(v1b1.Spec.Request)
		if err != nil {
			return nil, fmt.Errorf("v1beta1 csr invalid: %w", err)
		}
	}
	return &result, nil
}

func parseCertificateRequest(raw []byte) (*x509.CertificateRequest, error) {
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("decode CSR PEM block")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse CSR: %w", err)
	}
	return csr, nil
}

func validateV1(v1 *certv1.CertificateSigningRequest) error {
	if v1.Name == "" {
		return fmt.Errorf("v1 CertificateSigningRequest meta.Name is empty")
	}
	if v1.Spec.Request == nil {
		return fmt.Errorf("v1 CertificateSigningRequest spec.Request is nil")
	}
	if v1.Spec.SignerName == "" {
		return fmt.Errorf("v1 CertificateSigningRequest spec.SignerName is empty")
	}
	if v1.Spec.Username == "" {
		return fmt.Errorf("v1 CertificateSigningRequest spec.Username is empty")
	}
	if len(v1.Spec.Usages) == 0 {
		return fmt.Errorf("v1 CertificateSigningRequest spec.Usages is empty")
	}
	return nil
}

func validateV1Beta1(v1b1 *certv1beta1.CertificateSigningRequest) error {
	if v1b1.Name == "" {
		return fmt.Errorf("v1beta1 CertificateSigningRequest meta.Name is empty")
	}
	if v1b1.Spec.Request == nil {
		return fmt.Errorf("v1beta1 CertificateSigningRequest spec.Request is nil")
	}
	if v1b1.Spec.SignerName == nil {
		return fmt.Errorf("v1beta1 CertificateSigningRequest spec.SignerName is nil")
	}
	if *v1b1.Spec.SignerName == "" {
		return fmt.Errorf("v1beta1 CertificateSigningRequest spec.SignerName is empty")
	}
	if v1b1.Spec.Username == "" {
		return fmt.Errorf("v1beta1 CertificateSigningRequest spec.Username is empty")
	}
	if len(v1b1.Spec.Usages) == 0 {
		return fmt.Errorf("v1beta1 CertificateSigningRequest spec.Usages is empty")
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

// CreatedAt reads and returns the creation timestamp of the CertificateRequest from v1 or v1beta1.
func (f *CSR) CreatedAt() time.Time {
	if f.v1 != nil {
		return f.v1.CreationTimestamp.Time
	}
	return f.v1beta1.CreationTimestamp.Time
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

func (f CSR) Groups() []string {
	if f.v1 != nil {
		return f.v1.Spec.Groups
	}
	return f.v1beta1.Spec.Groups
}

// ParsedCertificateRequest returns the CertificateRequest parsed from v1 or v1beta1.
func (f *CSR) ParsedCertificateRequest() *x509.CertificateRequest {
	return f.parsed
}

// Approve add approved condition to the CertificateRequest if it is not already approved.
func (f *CSR) Approve(ctx context.Context, message string) error {
	if f.v1 != nil {
		return f.approveV1(ctx, message)
	}
	return f.approveV1Beta1(ctx, message)
}

func isAlreadyApprovedError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), fmt.Sprintf("Duplicate value: \"%s\"", certv1.CertificateApproved))
}

func (f *CSR) approveV1(ctx context.Context, message string) error {
	csr := f.v1.DeepCopy()
	csr.Status.Conditions = append(csr.Status.Conditions, certv1.CertificateSigningRequestCondition{
		LastUpdateTime: metav1.Now(),
		Message:        message,
		Reason:         "AutoApproved",
		Status:         v1.ConditionTrue,
		Type:           certv1.CertificateApproved,
	})
	csr, err := f.certificatesV1.CertificateSigningRequests().UpdateApproval(ctx, csr.Name, csr, metav1.UpdateOptions{})
	if isAlreadyApprovedError(err) {
		return nil
	}
	f.v1 = csr
	return err
}

func (f *CSR) approveV1Beta1(ctx context.Context, message string) error {
	csr := f.v1beta1.DeepCopy()
	csr.Status.Conditions = append(csr.Status.Conditions, certv1beta1.CertificateSigningRequestCondition{
		LastUpdateTime: metav1.Now(),
		Message:        message,
		Reason:         "AutoApproved",
		Status:         v1.ConditionTrue,
		Type:           certv1beta1.CertificateApproved,
	})
	csr, err := f.certificatesV1beta1.CertificateSigningRequests().UpdateApproval(ctx, csr, metav1.UpdateOptions{})
	if isAlreadyApprovedError(err) {
		return nil
	}
	f.v1beta1 = csr
	return err
}

func (c *CSR) Delete(ctx context.Context) error {
	if c.v1 != nil {
		return c.certificatesV1.CertificateSigningRequests().Delete(ctx, c.v1.Name, metav1.DeleteOptions{})
	}
	return c.certificatesV1beta1.CertificateSigningRequests().Delete(ctx, c.v1beta1.Name, metav1.DeleteOptions{})
}

// CreateOrRefresh creates the CertificateSigningRequest if it does not exist.
// If it does exist, it refreshes internally stored CSR object.
func (c *CSR) CreateOrRefresh(ctx context.Context) error {
	if c.v1 != nil {
		return c.createOrRefreshV1(ctx)
	}
	return c.createOrRefreshV1beta1(ctx)
}

func (c *CSR) createOrRefreshV1(ctx context.Context) error {
	csr := &certv1.CertificateSigningRequest{
		TypeMeta: metav1.TypeMeta{Kind: "CertificateSigningRequest"},
		ObjectMeta: metav1.ObjectMeta{
			Name: c.v1.Name,
		},
		Spec: certv1.CertificateSigningRequestSpec{
			SignerName:        c.v1.Spec.SignerName,
			Request:           c.v1.Spec.Request,
			Usages:            c.v1.Spec.Usages,
			ExpirationSeconds: c.v1.Spec.ExpirationSeconds,
		},
	}
	csr, err := c.certificatesV1.CertificateSigningRequests().Create(ctx, csr, metav1.CreateOptions{})
	if err != nil {
		if k8serrors.IsAlreadyExists(err) {
			csr, err = c.certificatesV1.CertificateSigningRequests().Get(ctx, c.v1.Name, metav1.GetOptions{})
			if err != nil {
				return err
			}
			c.v1 = csr
			return nil
		}
		return err
	}
	c.v1 = csr
	return nil
}

func (c *CSR) createOrRefreshV1beta1(ctx context.Context) error {
	csr := &certv1beta1.CertificateSigningRequest{
		TypeMeta: metav1.TypeMeta{Kind: "CertificateSigningRequest"},
		ObjectMeta: metav1.ObjectMeta{
			Name: c.v1beta1.Name,
		},
		Spec: certv1beta1.CertificateSigningRequestSpec{
			SignerName:        c.v1beta1.Spec.SignerName,
			Request:           c.v1beta1.Spec.Request,
			Usages:            c.v1beta1.Spec.Usages,
			ExpirationSeconds: c.v1beta1.Spec.ExpirationSeconds,
		},
	}
	csr, err := c.certificatesV1beta1.CertificateSigningRequests().Create(ctx, csr, metav1.CreateOptions{})
	if err != nil {
		if k8serrors.IsAlreadyExists(err) {
			csr, err := c.certificatesV1beta1.CertificateSigningRequests().Get(ctx, c.v1beta1.Name, metav1.GetOptions{})
			if err != nil {
				return err
			}
			c.v1beta1 = csr
			return nil
		}
		return err
	}
	c.v1beta1 = csr
	return nil
}
