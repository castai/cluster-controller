package csr

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"sort"
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
	csrInformerResyncPeriod = 12 * time.Hour
)

var ErrNodeCertificateNotFound = errors.New("node certificate not found")

// Certificate wraps v1 and v1beta1 csr.
type Certificate struct {
	v1             *certv1.CertificateSigningRequest
	v1Beta1        *certv1beta1.CertificateSigningRequest
	Name           string
	RequestingUser string
}

var errCSRNotFound = errors.New("v1 or v1beta csr should be set")

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

// GetCertificateByNodeName lists all csr objects and parses request pem encoded cert to find it by node name.
func GetCertificateByNodeName(ctx context.Context, client kubernetes.Interface, nodeName string) (*Certificate, error) {
	v1req, err := getNodeCSRV1(ctx, client, nodeName)
	if err != nil && !apierrors.IsNotFound(err) {
		return nil, err
	}
	if v1req != nil {
		return v1req, nil
	}

	v1betareq, err := getNodeCSRV1Beta1(ctx, client, nodeName)
	if err != nil {
		return nil, err
	}
	return v1betareq, nil
}

func getNodeCSRV1(ctx context.Context, client kubernetes.Interface, nodeName string) (*Certificate, error) {
	csrList, err := client.CertificatesV1().CertificateSigningRequests().List(ctx, getOptions(certv1.KubeAPIServerClientKubeletSignerName))
	if err != nil {
		return nil, err
	}

	// Sort by newest first soo we don't need to parse old items.
	sort.Slice(csrList.Items, func(i, j int) bool {
		return csrList.Items[i].CreationTimestamp.After(csrList.Items[j].CreationTimestamp.Time)
	})

	for _, csr := range csrList.Items {
		csr := csr
		sn, err := getSubjectCommonName(csr.Name, csr.Spec.Request)
		if err != nil {
			return nil, err
		}
		if sn == fmt.Sprintf("system:node:%s", nodeName) {
			return &Certificate{v1: &csr}, nil
		}
	}

	return nil, ErrNodeCertificateNotFound
}

func getNodeCSRV1Beta1(ctx context.Context, client kubernetes.Interface, nodeName string) (*Certificate, error) {
	csrList, err := client.CertificatesV1beta1().CertificateSigningRequests().
		List(ctx, getOptions(certv1beta1.KubeAPIServerClientKubeletSignerName))
	if err != nil {
		return nil, err
	}

	// Sort by newest first soo we don't need to parse old items.
	sort.Slice(csrList.Items, func(i, j int) bool {
		return csrList.Items[i].GetCreationTimestamp().After(csrList.Items[j].GetCreationTimestamp().Time)
	})

	for _, csr := range csrList.Items {
		sn, err := getSubjectCommonName(csr.Name, csr.Spec.Request)
		if err != nil {
			return nil, err
		}
		if sn == fmt.Sprintf("system:node:%s", nodeName) {
			return &Certificate{
				v1Beta1: &csr,
			}, nil
		}
	}

	return nil, ErrNodeCertificateNotFound
}

func WatchCastAINodeCSRs(ctx context.Context, log logrus.FieldLogger, client kubernetes.Interface, c chan<- *Certificate) error {
	v1Factory := informers.NewSharedInformerFactoryWithOptions(client, csrInformerResyncPeriod,
		informers.WithTweakListOptions(func(opts *metav1.ListOptions) {
			opts.FieldSelector = getOptions(certv1.KubeAPIServerClientKubeletSignerName).FieldSelector
		}))
	v1Informer := v1Factory.Certificates().V1().CertificateSigningRequests().Informer()

	v1beta1Factory := informers.NewSharedInformerFactoryWithOptions(client, csrInformerResyncPeriod,
		informers.WithTweakListOptions(func(opts *metav1.ListOptions) {
			opts.FieldSelector = getOptions(certv1beta1.KubeAPIServerClientKubeletSignerName).FieldSelector
		}))
	v1betaInformer := v1beta1Factory.Certificates().V1beta1().CertificateSigningRequests().Informer()

	handlerFuncs := cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if err := processCSREvent(ctx, c, obj); err != nil {
				log.WithError(err).Warn("failed to process csr add event")
			}
		},
		UpdateFunc: func(oldObj, newObj interface{}) {
			if err := processCSREvent(ctx, c, newObj); err != nil {
				log.WithError(err).Warn("failed to process csr update event")
			}
		},
		DeleteFunc: func(obj interface{}) {},
	}

	if _, err := v1Informer.AddEventHandler(handlerFuncs); err != nil {
		return fmt.Errorf("adding v1/csr informer event handlers: %w", err)
	}

	if _, err := v1betaInformer.AddEventHandler(handlerFuncs); err != nil {
		return fmt.Errorf("adding v1beta1/csr informer event handlers: %w", err)
	}

	stopCh := make(chan struct{})
	defer close(stopCh)

	go v1Factory.Start(stopCh)
	go v1beta1Factory.Start(stopCh)

	log.Info("watching for new node csr")

	<-ctx.Done()
	log.WithField("context", ctx.Err()).Info("finished watching for new node csr")
	return nil
}

var (
	errUnexpectedObjectType = errors.New("unexpected object type")
	errCSRTooOld            = errors.New("csr is too old")
	errOwner                = errors.New("owner is not bootstrap")
	errNonCastAINode        = errors.New("not a castai node")
)

func processCSREvent(ctx context.Context, c chan<- *Certificate, csrObj interface{}) error {
	cert, err := toCertificate(csrObj)
	if err != nil {
		return err
	}

	if cert == nil {
		return nil
	}

	if cert.Approved() {
		return nil
	}

	sendCertificate(ctx, c, cert)
	return nil
}

func toCertificate(obj interface{}) (cert *Certificate, err error) {
	var name string
	var request []byte

	isOutdated := false
	switch e := obj.(type) {
	case *certv1.CertificateSigningRequest:
		name = e.Name
		request = e.Spec.Request
		cert = &Certificate{Name: name, v1: e, RequestingUser: e.Spec.Username}
		isOutdated = e.CreationTimestamp.Add(csrTTL).Before(time.Now())
	case *certv1beta1.CertificateSigningRequest:
		name = e.Name
		request = e.Spec.Request
		cert = &Certificate{Name: name, v1Beta1: e, RequestingUser: e.Spec.Username}
		isOutdated = e.CreationTimestamp.Add(csrTTL).Before(time.Now())
	default:
		return nil, errUnexpectedObjectType
	}

	if isOutdated {
		return nil, fmt.Errorf("csr with certificate Name: %v RequestingUser: %v %w", cert.Name, cert.RequestingUser, errCSRTooOld)
	}

	// Since we only have one handler per CSR/certificate name,
	// which is the node name, we can process the controller's certificates and kubelet-bootstrap`s.
	// This covers the case when the controller restarts but the bootstrap certificate was deleted without our own certificate being approved.
	if cert.RequestingUser != "kubelet-bootstrap" && cert.RequestingUser != "system:serviceaccount:castai-agent:castai-cluster-controller" {
		return nil, fmt.Errorf("csr with certificate Name: %v RequestingUser: %v %w", cert.Name, cert.RequestingUser, errOwner)
	}

	cn, err := getSubjectCommonName(name, request)
	if err != nil {
		return nil, fmt.Errorf("getSubjectCommonName: Name: %v RequestingUser: %v  request: %v %w", cert.Name, cert.RequestingUser, string(request), err)
	}

	if !isCastAINodeCsr(cn) {
		return nil, fmt.Errorf("csr with certificate Name: %v RequestingUser: %v cn: %v %w", cert.Name, cert.RequestingUser, cn, errNonCastAINode)
	}
	cert.Name = cn

	return cert, nil
}

func isCastAINodeCsr(subjectCommonName string) bool {
	if subjectCommonName == "" {
		return false
	}

	if strings.HasPrefix(subjectCommonName, "system:node") && strings.Contains(subjectCommonName, "cast-pool") {
		return true
	}

	return false
}

func sendCertificate(ctx context.Context, c chan<- *Certificate, cert *Certificate) {
	select {
	case c <- cert:
	case <-ctx.Done():
		return
	}
}

func getSubjectCommonName(csrName string, csrRequest []byte) (string, error) {
	if !strings.HasPrefix(csrName, "node-csr") {
		return "", nil
	}

	certReq, err := parseCSR(csrRequest)
	if err != nil {
		return "", err
	}
	return certReq.Subject.CommonName, nil
}

// parseCSR is mostly needed to extract node name from cert subject common name.
func parseCSR(pemData []byte) (*x509.CertificateRequest, error) {
	block, _ := pem.Decode(pemData)
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return nil, fmt.Errorf("PEM block type must be CERTIFICATE REQUEST")
	}
	return x509.ParseCertificateRequest(block.Bytes)
}

//nolint:unparam
func getOptions(signer string) metav1.ListOptions {
	fields.SelectorFromSet(fields.Set{})

	return metav1.ListOptions{
		FieldSelector: fields.SelectorFromSet(fields.Set{
			"spec.signerName": signer,
		}).String(),
	}
}
