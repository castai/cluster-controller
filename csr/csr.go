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
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"

	"github.com/castai/cluster-controller/castai"
	"github.com/castai/cluster-controller/waitext"
)

const (
	ReasonApproved  = "AutoApproved"
	approvedMessage = "This CSR was approved by CAST AI"
)

var (
	ErrNodeCertificateNotFound = errors.New("node certificate not found")
)

// Certificate wraps v1 and v1beta1 csr.
type Certificate struct {
	V1      *certv1.CertificateSigningRequest
	V1Beta1 *certv1beta1.CertificateSigningRequest
	Name    string
}

func (c *Certificate) Validate() error {
	if c.V1 == nil && c.V1Beta1 == nil {
		return errors.New("v1 or v1beta csr should be set")
	}
	return nil
}

func (c *Certificate) Approved() bool {
	if c.V1Beta1 != nil {
		for _, condition := range c.V1Beta1.Status.Conditions {
			if condition.Reason == ReasonApproved {
				return true
			}
		}
		return false
	}

	for _, condition := range c.V1.Status.Conditions {
		if condition.Reason == ReasonApproved && condition.Status == v1.ConditionTrue {
			return true
		}
	}
	return false
}

// ApproveCertificate approves csr.
func (cert *Certificate) ApproveCertificate(ctx context.Context, client kubernetes.Interface) (*Certificate, error) {
	if err := cert.Validate(); err != nil {
		return nil, err
	}

	if cert.V1Beta1 != nil {
		cert.V1Beta1.Status.Conditions = append(cert.V1Beta1.Status.Conditions, certv1beta1.CertificateSigningRequestCondition{
			Type:           certv1beta1.CertificateApproved,
			Reason:         ReasonApproved,
			Message:        approvedMessage,
			LastUpdateTime: metav1.Now(),
		})
		resp, err := client.CertificatesV1beta1().CertificateSigningRequests().UpdateApproval(ctx, cert.V1Beta1, metav1.UpdateOptions{})
		if err != nil {
			return nil, fmt.Errorf("v1beta csr approve: %w", err)
		}
		return &Certificate{V1Beta1: resp}, nil
	}

	cert.V1.Status.Conditions = append(cert.V1.Status.Conditions, certv1.CertificateSigningRequestCondition{
		Type:           certv1.CertificateApproved,
		Reason:         ReasonApproved,
		Message:        approvedMessage,
		Status:         v1.ConditionTrue,
		LastUpdateTime: metav1.Now(),
	})
	resp, err := client.CertificatesV1().CertificateSigningRequests().UpdateApproval(ctx, cert.V1.Name, cert.V1, metav1.UpdateOptions{})
	if err != nil {
		return nil, fmt.Errorf("v1 csr approve: %w", err)
	}
	return &Certificate{V1: resp}, nil
}

// DeleteCertificate deletes csr.
func (c *Certificate) DeleteCertificate(ctx context.Context, client kubernetes.Interface) error {
	if err := c.Validate(); err != nil {
		return err
	}

	if c.V1Beta1 != nil {
		return client.CertificatesV1beta1().CertificateSigningRequests().Delete(ctx, c.V1Beta1.Name, metav1.DeleteOptions{})
	}
	return client.CertificatesV1().CertificateSigningRequests().Delete(ctx, c.V1.Name, metav1.DeleteOptions{})
}

// RequestCertificate creates new csr.
func (c *Certificate) RequestNewCertificate(ctx context.Context, client kubernetes.Interface) (*Certificate, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}

	if c.V1Beta1 != nil {
		resp, err := createv1beta(ctx, client, c.V1Beta1)
		if err != nil {
			if apierrors.IsAlreadyExists(err) {
				return get(ctx, client, c)
			}
			return nil, fmt.Errorf("v1beta csr create: %w", err)
		}
		return &Certificate{V1Beta1: resp}, nil
	}

	resp, err := createv1(ctx, client, c.V1)
	if err != nil {
		if apierrors.IsAlreadyExists(err) {
			return get(ctx, client, c)
		}
		return nil, fmt.Errorf("v1 csr create: %w", err)
	}
	return &Certificate{V1: resp}, nil
}

func get(ctx context.Context, client kubernetes.Interface, cert *Certificate) (*Certificate, error) {
	if cert.V1Beta1 != nil {
		v1beta1req, err := client.CertificatesV1beta1().CertificateSigningRequests().Get(ctx, cert.V1Beta1.Name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		return &Certificate{V1Beta1: v1beta1req}, nil
	}

	v1req, err := client.CertificatesV1().CertificateSigningRequests().Get(ctx, cert.V1.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	return &Certificate{V1: v1req}, nil
}

func createv1(ctx context.Context, client kubernetes.Interface, csr *certv1.CertificateSigningRequest) (*certv1.CertificateSigningRequest, error) {
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

func createv1beta(ctx context.Context, client kubernetes.Interface, csr *certv1beta1.CertificateSigningRequest) (*certv1beta1.CertificateSigningRequest, error) {
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
			return &Certificate{V1: &csr}, nil
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
				V1Beta1: &csr,
			}, nil
		}
	}

	return nil, ErrNodeCertificateNotFound
}

func WatchCastAINodeCSRs(ctx context.Context, log logrus.FieldLogger, client kubernetes.Interface, c chan *Certificate) {
	var w watch.Interface
	var err error
	b := waitext.DefaultExponentialBackoff()
	err = waitext.Retry(
		ctx,
		b,
		waitext.Forever,
		func(ctx context.Context) (bool, error) {
			w, err = getWatcher(ctx, client)
			if err != nil {
				return true, fmt.Errorf("fail to open v1 and v1beta watching client: %w", err)
			}
			return false, nil
		},
		func(err error) {
			log.Warnf("retrying: %v", err)
		},
	)
	if err != nil {
		log.Warnf("finished: %v", err)
		return
	}

	defer w.Stop()

	log.Debug("watching for new node csr")

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-w.ResultChan():
			if !ok {
				WatchCastAINodeCSRs(ctx, log, client, c) // start over in case of any error
			}

			var name string
			var request []byte
			var csrResult *Certificate
			switch csr := event.Object.(type) {
			case *certv1.CertificateSigningRequest:
				name = csr.Name
				request = csr.Spec.Request
				csrResult = &Certificate{V1: csr}
			case *certv1beta1.CertificateSigningRequest:
				name = csr.Name
				request = csr.Spec.Request
				csrResult = &Certificate{V1Beta1: csr}
			default:
				continue
			}

			cn, err := getSubjectCommonName(name, request)
			if err != nil {
				log.WithFields(logrus.Fields{
					"csr":       name,
					"node_name": cn,
				}).Debugf("WatchAndApproveNodeCSRV1: skipping csr: %v", err)
				continue
			}
			if csrResult.Approved() || !isAutoApproveAllowedForNode(ctx, client, cn) {
				continue
			}
			csrResult.Name = cn
			sendCertificate(ctx, c, csrResult)
		}
	}
}

func getWatcher(ctx context.Context, client kubernetes.Interface) (watch.Interface, error) {
	w, err := client.CertificatesV1().CertificateSigningRequests().Watch(ctx, getOptions(certv1.KubeAPIServerClientKubeletSignerName))
	if err != nil {
		w, err = client.CertificatesV1beta1().CertificateSigningRequests().Watch(ctx, getOptions(certv1beta1.KubeAPIServerClientKubeletSignerName))
		if err != nil {
			return nil, fmt.Errorf("fail to open v1 and v1beta watching client: %w", err)
		}
	}
	return w, nil
}

func isAutoApproveAllowedForNode(ctx context.Context, client kubernetes.Interface, nodeName string) bool {
	if nodeName == "" {
		return false
	}
	n, err := client.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil || n == nil {
		return false
	}
	managedBy, ok := n.Labels[castai.LabelManagedBy]
	if !ok {
		return false
	}
	if managedBy != castai.LabelValueManagedByCASTAI {
		return false
	}

	if n.CreationTimestamp.After(time.Now().Add(-time.Hour * 24)) {
		return false
	}

	return true
}

func sendCertificate(ctx context.Context, c chan *Certificate, cert *Certificate) {
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

func getOptions(signer string) metav1.ListOptions {
	return metav1.ListOptions{
		FieldSelector: fields.SelectorFromSet(fields.Set{
			"spec.signerName": signer,
		}).String(),
	}
}
