package csr

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	certv1 "k8s.io/api/certificates/v1"
	certv1beta1 "k8s.io/api/certificates/v1beta1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

const (
	ReasonApproved  = "AutoApproved"
	approvedMessage = "This CSR was approved by CAST AI"
	csrTTL          = time.Hour

	// We should approve CSRs, when they are created, so resync can be high.
	// Resync plays back all events (create, update, delete), which are in informer cache.
	// This does not involve talking to API server, it is not relist.
	csrInformerResyncPeriod = 12 * time.Hour
)

var ErrNodeCertificateNotFound = errors.New("node certificate not found")

// Certificate wraps v1 and v1beta1 csr.
type Certificate struct {
	v1              *certv1.CertificateSigningRequest
	v1Beta1         *certv1beta1.CertificateSigningRequest
	Name            string
	OriginalCSRName string
	RequestingUser  string
	SignerName      string
	Usages          []string
}

var (
	errCSRNotFound = errors.New("v1 or v1beta csr should be set")
	errInvalidCSR  = errors.New("invalid CSR")
)

func (c *Certificate) Validate() error {
	if c.v1 == nil && c.v1Beta1 == nil {
		return errCSRNotFound
	}
	return nil
}

func (c *Certificate) Approved() bool {
	if c.v1Beta1 != nil {
		for _, condition := range c.v1Beta1.Status.Conditions {
			if condition.Reason == ReasonApproved {
				return true
			}
		}
		return false
	}

	for _, condition := range c.v1.Status.Conditions {
		if condition.Reason == ReasonApproved && condition.Status == v1.ConditionTrue {
			return true
		}
	}
	return false
}

// Outdated returns, whether the certificate request is old and should not be processed by cluster-controller.
// It has nothing to do with certificate expiration.
func (c *Certificate) Outdated() bool {
	if c.v1Beta1 != nil {
		return c.v1Beta1.CreationTimestamp.Add(csrTTL).Before(time.Now())
	}
	return c.v1.CreationTimestamp.Add(csrTTL).Before(time.Now())
}

func (c *Certificate) ForCASTAINode() bool {
	if c.Name == "" {
		return false
	}

	if strings.HasPrefix(c.Name, "system:node") && strings.Contains(c.Name, "cast-pool") {
		return true
	}

	return false
}

func (c *Certificate) isRequestedByNodeBootstrap() bool {
	// Since we only have one handler per CSR/certificate name,
	// which is the node name, we can process the controller's certificates and kubelet-bootstrap`s.
	// This covers the case when the controller restarts but the bootstrap certificate was deleted without our own certificate being approved.
	return c.RequestingUser == "kubelet-bootstrap" || c.RequestingUser == "system:serviceaccount:castai-agent:castai-cluster-controller"
}

func (c *Certificate) isRequestedBySystemNode() bool {
	// To avoid waiting for the certificate to be approved by control plane.
	// We can approve the certificate if it was requested by the system node.
	return strings.HasPrefix(c.RequestingUser, "system:node:")
}

func isAlreadyApproved(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "Duplicate value: \"Approved\"")
}

// ApproveCSRCertificate approves csr.
func (c *Certificate) ApproveCSRCertificate(ctx context.Context, client kubernetes.Interface) (*Certificate, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}

	if c.v1Beta1 != nil {
		c.v1Beta1.Status.Conditions = append(c.v1Beta1.Status.Conditions, certv1beta1.CertificateSigningRequestCondition{
			Type:           certv1beta1.CertificateApproved,
			Reason:         ReasonApproved,
			Message:        approvedMessage,
			LastUpdateTime: metav1.Now(),
		})
		resp, err := client.CertificatesV1beta1().CertificateSigningRequests().UpdateApproval(ctx, c.v1Beta1, metav1.UpdateOptions{})
		if err != nil && !isAlreadyApproved(err) {
			return nil, fmt.Errorf("v1beta csr approve: %w", err)
		}
		return &Certificate{v1Beta1: resp}, nil
	}

	c.v1.Status.Conditions = append(c.v1.Status.Conditions, certv1.CertificateSigningRequestCondition{
		Type:           certv1.CertificateApproved,
		Reason:         ReasonApproved,
		Message:        approvedMessage,
		Status:         v1.ConditionTrue,
		LastUpdateTime: metav1.Now(),
	})
	resp, err := client.CertificatesV1().CertificateSigningRequests().UpdateApproval(ctx, c.v1.Name, c.v1, metav1.UpdateOptions{})
	if err != nil && !isAlreadyApproved(err) {
		return nil, fmt.Errorf("v1 csr approve: %w", err)
	}
	return &Certificate{v1: resp}, nil
}

// DeleteCSR deletes csr.
func (c *Certificate) DeleteCSR(ctx context.Context, client kubernetes.Interface) error {
	if err := c.Validate(); err != nil {
		return err
	}

	if c.v1Beta1 != nil {
		return client.CertificatesV1beta1().CertificateSigningRequests().Delete(ctx, c.v1Beta1.Name, metav1.DeleteOptions{})
	}
	return client.CertificatesV1().CertificateSigningRequests().Delete(ctx, c.v1.Name, metav1.DeleteOptions{})
}

// NewCSR creates new csr.
func (c *Certificate) NewCSR(ctx context.Context, client kubernetes.Interface) (*Certificate, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}

	if c.v1Beta1 != nil {
		resp, err := createV1beta1(ctx, client, c.v1Beta1)
		if err != nil {
			if apierrors.IsAlreadyExists(err) {
				return get(ctx, client, c)
			}
			return nil, fmt.Errorf("v1beta csr create: %w", err)
		}
		return &Certificate{v1Beta1: resp}, nil
	}

	resp, err := createV1(ctx, client, c.v1)
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			return get(ctx, client, c)
		}
		return nil, fmt.Errorf("v1 csr create: %w", err)
	}

	return &Certificate{v1: resp}, nil
}

func startInformers(ctx context.Context, log logrus.FieldLogger, factories ...informers.SharedInformerFactory) {
	stopCh := make(chan struct{})
	defer close(stopCh)

	for _, factory := range factories {
		factory.Start(stopCh)
	}

	log.Info("watching for new node CSRs")

	<-ctx.Done()
	log.WithField("context", ctx.Err()).Info("finished watching for new node CSRs")
}

func get(ctx context.Context, client kubernetes.Interface, cert *Certificate) (*Certificate, error) {
	if cert.v1Beta1 != nil {
		v1beta1req, err := client.CertificatesV1beta1().CertificateSigningRequests().Get(ctx, cert.v1Beta1.Name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		return &Certificate{v1Beta1: v1beta1req}, nil
	}

	v1req, err := client.CertificatesV1().CertificateSigningRequests().Get(ctx, cert.v1.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return &Certificate{v1: v1req}, nil
}

func createV1(ctx context.Context, client kubernetes.Interface, csr *certv1.CertificateSigningRequest) (*certv1.CertificateSigningRequest, error) {
	csrv1 := &certv1.CertificateSigningRequest{
		// Username, UID, Groups will be injected by API server.
		TypeMeta: metav1.TypeMeta{Kind: "CertificateSigningRequest"},
		ObjectMeta: metav1.ObjectMeta{
			Name: csr.Name,
		},
		Spec: certv1.CertificateSigningRequestSpec{
			SignerName:        csr.Spec.SignerName,
			Request:           csr.Spec.Request,
			Usages:            csr.Spec.Usages,
			ExpirationSeconds: csr.Spec.ExpirationSeconds,
		},
	}
	req, err := client.CertificatesV1().CertificateSigningRequests().Create(ctx, csrv1, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}
	return req, nil
}

func createV1beta1(ctx context.Context, client kubernetes.Interface, csr *certv1beta1.CertificateSigningRequest) (*certv1beta1.CertificateSigningRequest, error) {
	v1beta1csr := &certv1beta1.CertificateSigningRequest{
		TypeMeta: metav1.TypeMeta{Kind: "CertificateSigningRequest"},
		ObjectMeta: metav1.ObjectMeta{
			Name: csr.Name,
		},
		Spec: certv1beta1.CertificateSigningRequestSpec{
			SignerName: csr.Spec.SignerName,
			Request:    csr.Spec.Request,
			Usages:     csr.Spec.Usages,
		},
	}

	req, err := client.CertificatesV1beta1().CertificateSigningRequests().Create(ctx, v1beta1csr, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}
	return req, nil
}

func createInformer(ctx context.Context, client kubernetes.Interface, fieldSelectorV1, fieldSelectorV1beta1 string) (informers.SharedInformerFactory, cache.SharedIndexInformer, error) {
	var (
		errv1      error
		errv1beta1 error
	)

	if _, errv1 = client.CertificatesV1().CertificateSigningRequests().List(ctx, metav1.ListOptions{}); errv1 == nil {
		v1Factory := informers.NewSharedInformerFactoryWithOptions(client, csrInformerResyncPeriod,
			informers.WithTweakListOptions(func(opts *metav1.ListOptions) {
				opts.FieldSelector = fieldSelectorV1
			}))
		v1Informer := v1Factory.Certificates().V1().CertificateSigningRequests().Informer()
		return v1Factory, v1Informer, nil
	}

	if _, errv1beta1 = client.CertificatesV1beta1().CertificateSigningRequests().List(ctx, metav1.ListOptions{}); errv1beta1 == nil {
		v1Factory := informers.NewSharedInformerFactoryWithOptions(client, csrInformerResyncPeriod,
			informers.WithTweakListOptions(func(opts *metav1.ListOptions) {
				opts.FieldSelector = fieldSelectorV1beta1
			}))
		v1Informer := v1Factory.Certificates().V1beta1().CertificateSigningRequests().Informer()
		return v1Factory, v1Informer, nil
	}

	return nil, nil, fmt.Errorf("failed to create informer: v1: %w, v1beta1: %w", errv1, errv1beta1)
}

var errUnexpectedObjectType = errors.New("unexpected object type")

func processCSREvent(ctx context.Context, c chan<- *Certificate, csrObj interface{}) error {
	cert, err := toCertificate(csrObj)
	if err != nil {
		return fmt.Errorf("toCertificate: %w", err)
	}

	if cert == nil {
		return nil
	}

	if cert.Approved() ||
		!cert.ForCASTAINode() ||
		// approve only node bootstrap and kubelet CSR from node.
		(!cert.isRequestedBySystemNode() && !cert.isRequestedByNodeBootstrap()) ||
		cert.Outdated() {
		return nil
	}

	sendCertificate(ctx, c, cert)
	return nil
}

func toCertificate(obj interface{}) (cert *Certificate, err error) {
	var request []byte

	switch e := obj.(type) {
	case *certv1.CertificateSigningRequest:
		request = e.Spec.Request
		cert = &Certificate{
			OriginalCSRName: e.Name,
			SignerName:      e.Spec.SignerName,
			v1:              e,
			RequestingUser:  e.Spec.Username,
			Usages:          toKeyUsage(e.Spec.Usages),
		}
	case *certv1beta1.CertificateSigningRequest:
		request = e.Spec.Request
		cert = &Certificate{
			OriginalCSRName: e.Name,
			v1Beta1:         e,
			RequestingUser:  e.Spec.Username,
			Usages:          toKeyUsage(e.Spec.Usages),
		}
		if e.Spec.SignerName != nil {
			cert.SignerName = *e.Spec.SignerName
		}
	default:
		return nil, errUnexpectedObjectType
	}

	cn, err := cert.getSubjectCommonName(request)
	if err != nil {
		return nil, fmt.Errorf("getSubjectCommonName: Name: %v RequestingUser: %v  request: %v %w", cert.OriginalCSRName, cert.RequestingUser, string(request), err)
	}

	cert.Name = cn

	return cert, nil
}

func sendCertificate(ctx context.Context, c chan<- *Certificate, cert *Certificate) {
	select {
	case c <- cert:
	case <-ctx.Done():
		return
	}
}

func (c *Certificate) getSubjectCommonName(csrRequest []byte) (string, error) {
	// node-csr prefix for bootstrap kubelet csr.
	// csr- prefix for kubelet csr.
	if !strings.HasPrefix(c.OriginalCSRName, "node-csr") && !strings.HasPrefix(c.OriginalCSRName, "csr-") {
		return "", fmt.Errorf("invalid CSR name: %s %w", c.OriginalCSRName, errInvalidCSR)
	}

	certReq, err := c.parseCSR(csrRequest)
	if err != nil {
		return "", err
	}
	return certReq.Subject.CommonName, nil
}

// parseCSR is mostly needed to extract node name from cert subject common name.
func (c *Certificate) parseCSR(pemData []byte) (*x509.CertificateRequest, error) {
	block, _ := pem.Decode(pemData)
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return nil, fmt.Errorf("PEM block type must be CERTIFICATE REQUEST")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse certificate request: %w", err)
	}
	if err := c.validateCSR(csr); err != nil {
		return nil, fmt.Errorf("validate CSR: %w", err)
	}
	return csr, nil
}

func (c *Certificate) validateCSR(csr *x509.CertificateRequest) error {
	if csr == nil {
		return fmt.Errorf("%w: nil CSR", errInvalidCSR)
	}
	if c.SignerName == certv1.KubeAPIServerClientKubeletSignerName {
		// no validation
		return nil
	}
	if c.SignerName != certv1.KubeletServingSignerName {
		return fmt.Errorf("%w: unknown signer name %s", errInvalidCSR, c.SignerName)
	}

	if len(csr.Subject.CommonName) == 0 {
		return fmt.Errorf("%w: CSR subject common name", errInvalidCSR)
	}
	if len(csr.URIs) > 0 {
		return fmt.Errorf("%w: CSR subject URIs must be empty: %v", errInvalidCSR, csr.URIs)
	}
	if len(csr.EmailAddresses) > 0 {
		return fmt.Errorf("%w: CSR subject email addresses must be empty: %v", errInvalidCSR, csr.EmailAddresses)
	}

	if len(c.Usages) == 0 {
		return fmt.Errorf("%w: CSR Usages is empty", errInvalidCSR)
	}
	usageServerAuthExisted := false
	for _, u := range c.Usages {
		if u != fmt.Sprintf("%v", certv1.UsageServerAuth) &&
			u != fmt.Sprintf("%v", certv1.UsageDigitalSignature) &&
			u != fmt.Sprintf("%v", certv1.UsageKeyEncipherment) {
			return fmt.Errorf("%v: CSR usages %w", c.Usages, errInvalidCSR)
		}
		if u == fmt.Sprintf("%v", certv1.UsageServerAuth) {
			usageServerAuthExisted = true
		}
	}
	if !usageServerAuthExisted {
		return fmt.Errorf("%w: CSR usages must be for server usage %v", errInvalidCSR, c.Usages)
	}
	// TODO add validation of IP and DNS
	// https://kubernetes.io/docs/reference/access-authn-authz/kubelet-tls-bootstrapping/#certificate-rotation

	return nil
}

//nolint:unparam
func getOptions(signer string) metav1.ListOptions {
	return metav1.ListOptions{
		FieldSelector: fields.SelectorFromSet(fields.Set{
			"spec.signerName": signer,
		}).String(),
	}
}

func toKeyUsage[T certv1.KeyUsage | certv1beta1.KeyUsage](usages []T) []string {
	u := make([]string, 0, len(usages))
	for _, usage := range usages {
		u = append(u, string(usage))
	}
	return u
}
