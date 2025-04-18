package csr

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/samber/lo"
	"github.com/sirupsen/logrus"
	certv1 "k8s.io/api/certificates/v1"
	certv1beta1 "k8s.io/api/certificates/v1beta1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	"github.com/castai/cluster-controller/internal/actions/csr/wrapper"
	"github.com/castai/cluster-controller/internal/waitext"
)

const (
	approveCSRTimeout              = 4 * time.Minute
	groupSystemNodesName           = "system:nodes"
	kubeletBootstrapRequestingUser = "kubelet-bootstrap"
	clusterControllerSAName        = "system:serviceaccount:castai-agent:castai-cluster-controller"
	approvedMessage                = "This CSR was approved by CAST AI"
	csrOutdatedAfter               = time.Hour
)

func NewApprovalManager(log logrus.FieldLogger, clientset kubernetes.Interface) *ApprovalManager {
	return &ApprovalManager{
		log:       log,
		clientset: clientset,
	}
}

type ApprovalManager struct {
	log               logrus.FieldLogger
	clientset         kubernetes.Interface
	cancelAutoApprove context.CancelFunc

	inProgress map[string]struct{} // one handler per csr/certificate Name.
	mu         sync.Mutex          // Used to make sure there is just one watcher running.
}

func (m *ApprovalManager) Start(ctx context.Context) error {
	informerKubeletSignerFactory, csrInformerKubeletSigner, err := createInformer(
		ctx,
		m.clientset,
		listOptionsWithSigner(certv1.KubeAPIServerClientKubeletSignerName).FieldSelector,
		listOptionsWithSigner(certv1beta1.KubeAPIServerClientKubeletSignerName).FieldSelector)
	if err != nil {
		return fmt.Errorf("while creating informer for %v: %w", certv1.KubeAPIServerClientKubeletSignerName, err)
	}

	informerKubeletServingFactory, csrInformerKubeletServing, err := createInformer(
		ctx,
		m.clientset,
		listOptionsWithSigner(certv1.KubeletServingSignerName).FieldSelector,
		listOptionsWithSigner(certv1beta1.KubeletServingSignerName).FieldSelector)
	if err != nil {
		return fmt.Errorf("while creating informer for %v: %w", certv1.KubeletServingSignerName, err)
	}

	c := make(chan *wrapper.CSR, 1)

	handlerFuncs := cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			csr, err := wrapper.NewCSR(m.clientset, obj)
			if err != nil {
				m.log.WithError(err).Warn("creating csr wrapper")
				return
			}
			select {
			case c <- csr:
			case <-ctx.Done():
				return
			}
		},
	}

	if _, err := csrInformerKubeletSigner.AddEventHandler(handlerFuncs); err != nil {
		return fmt.Errorf("adding csr informer event handlers: %w", err)
	}

	if _, err := csrInformerKubeletServing.AddEventHandler(handlerFuncs); err != nil {
		return fmt.Errorf("adding csr informer event handlers: %w", err)
	}

	ctx, cancel := context.WithCancel(ctx)
	if !m.startAutoApprove(cancel) {
		return nil
	}

	go startInformers(ctx, m.log, informerKubeletSignerFactory, informerKubeletServingFactory)
	go m.runAutoApproveForCastAINodes(ctx, c)

	if !cache.WaitForNamedCacheSync("cluster-controller/approval-manager", ctx.Done(), csrInformerKubeletSigner.HasSynced, csrInformerKubeletServing.HasSynced) {
		m.log.WithField("context", ctx.Err()).Info("stopping auto approve csr")
		return nil
	}

	return nil
}

func (m *ApprovalManager) Stop() {
	m.stopAutoApproveForCastAINodes()
}

func (m *ApprovalManager) startAutoApprove(cancelFunc context.CancelFunc) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancelAutoApprove != nil {
		return false
	}

	m.log.Info("starting auto approve CSRs for managed by CAST AI nodes")
	m.cancelAutoApprove = cancelFunc

	return true
}

func (m *ApprovalManager) runAutoApproveForCastAINodes(ctx context.Context, c <-chan *wrapper.CSR) {
	defer m.stopAutoApproveForCastAINodes()

	log := m.log.WithField("RunAutoApprove", "auto-approve-csr")

	for {
		select {
		case <-ctx.Done():
			log.WithError(ctx.Err()).Errorf("auto approve csr finished")
			return
		case csr := <-c:
			// prevent starting goroutine for the same node certificate
			if !m.addInProgress(csr.ParsedCertificateRequest().Subject.CommonName, csr.SignerName()) {
				continue
			}
			go func(csr *wrapper.CSR) {
				defer m.removeInProgress(csr.ParsedCertificateRequest().Subject.CommonName, csr.SignerName())

				log := log.WithFields(logrus.Fields{
					"CN":     csr.ParsedCertificateRequest().Subject.CommonName,
					"signer": csr.SignerName(),
					"csr":    csr.Name(),
				})
				if shouldSkip(log, csr) {
					log.Debug("skipping csr")
					return
				}
				log.Info("auto approving csr")
				err := m.handleWithRetry(ctx, log, csr)
				if err != nil {
					log.WithError(err).Errorf("failed to approve csr: %+v", csr)
				}
			}(csr)
		}
	}
}

func (m *ApprovalManager) handleWithRetry(ctx context.Context, log *logrus.Entry, csr *wrapper.CSR) error {
	ctx, cancel := context.WithTimeout(ctx, approveCSRTimeout)
	defer cancel()

	b := newApproveCSRExponentialBackoff()
	return waitext.Retry(
		ctx,
		b,
		waitext.Forever,
		func(ctx context.Context) (bool, error) {
			return true, m.handle(ctx, log, csr)
		},
		func(err error) {
			log.Warnf("csr approval failed, will retry: %v", err)
		},
	)
}

func newApproveCSRExponentialBackoff() wait.Backoff {
	b := waitext.DefaultExponentialBackoff()
	b.Factor = 2
	return b
}

func (m *ApprovalManager) handle(ctx context.Context, log logrus.FieldLogger, csr *wrapper.CSR) (reterr error) {
	if err := m.validateCSRRequirements(ctx, csr); err != nil {
		return fmt.Errorf("validating csr: %w", err)
	}

	// Create a new CSR with the same request data as the original one,
	// since the old csr maybe denied.
	log.Info("requesting new csr if doesn't exist")
	err := csr.CreateOrRefresh(ctx)
	if err != nil {
		return fmt.Errorf("requesting new csr if doesn't exist: %w", err)
	}

	// Approve csr.
	log.Info("approving csr")
	err = csr.Approve(ctx, approvedMessage)
	if err != nil {
		return fmt.Errorf("approving csr: %w", err)
	}
	if csr.Approved() {
		return nil
	}

	// clean original csr. should be the last step for having the possibility.
	// continue approving csr: old deleted-> restart-> node never join.
	log.Info("deleting old csr")
	if err := csr.Delete(ctx); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("deleting csr: %w", err)
		}
	}

	return errors.New("certificate signing request was not approved")
}

func shouldSkip(log logrus.FieldLogger, csr *wrapper.CSR) bool {
	if csr.Approved() {
		log.Debug("csr already approved")
		return true
	}
	if time.Since(csr.CreatedAt()) > csrOutdatedAfter {
		log.Debug("csr is outdated")
		return true
	}
	if !managedSigner(csr.SignerName()) {
		log.Debug("csr unknown signer")
		return true
	}
	if !managedCSRNamePrefix(csr.Name()) {
		log.Debug("csr name not managed by CAST AI: ", csr.Name())
		return true
	}
	if !managedCSRRequestingUser(csr.RequestingUser()) {
		log.Debug("csr requesting user is not managed by CAST AI")
		return true
	}
	if !managerSubjectCommonName(csr.ParsedCertificateRequest().Subject.CommonName) {
		log.Debug("csr common name is not managed by CAST AI")
		return true
	}
	return false
}

func managedSigner(signerName string) bool {
	return signerName == certv1.KubeAPIServerClientKubeletSignerName ||
		signerName == certv1.KubeletServingSignerName
}

func managedCSRNamePrefix(n string) bool {
	return strings.HasPrefix(n, "node-csr-") || strings.HasPrefix(n, "csr-")
}

func managedCSRRequestingUser(s string) bool {
	return s == kubeletBootstrapRequestingUser || s == clusterControllerSAName || strings.HasPrefix(s, "system:node:")
}

func managerSubjectCommonName(commonName string) bool {
	return strings.HasPrefix(commonName, "system:node:") && strings.Contains(commonName, "cast-pool")
}

func (m *ApprovalManager) validateCSRRequirements(ctx context.Context, csr *wrapper.CSR) error {
	switch csr.SignerName() {
	case certv1.KubeAPIServerClientKubeletSignerName:
		return m.validateKubeletClientCSR(csr)
	case certv1.KubeletServingSignerName:
		return m.validateKubeletServingCSR(ctx, csr)
	default:
		// Unless logic changes this never returns because unknown signer csr's are skipped.
		return fmt.Errorf("unsupported signer name: %s", csr.SignerName())
	}
}

func (m *ApprovalManager) validateKubeletClientCSR(csr *wrapper.CSR) error {
	permitted := []string{
		string(certv1.UsageClientAuth),
		string(certv1.UsageKeyEncipherment),
		string(certv1.UsageDigitalSignature),
	}
	if notPermitted := lo.Without(csr.Usages(), permitted...); len(notPermitted) > 0 {
		return fmt.Errorf("CSR contains not permitted usages: %s", strings.Join(notPermitted, ", "))
	}
	return nil
}

func (m *ApprovalManager) validateKubeletServingCSR(ctx context.Context, csr *wrapper.CSR) error {
	// Implement validation suggested from https://kubernetes.io/docs/reference/access-authn-authz/kubelet-tls-bootstrapping/#certificate-rotation

	// Check for required group.
	if !lo.Contains(csr.Groups(), groupSystemNodesName) {
		return fmt.Errorf("CSR does not contain group %s", groupSystemNodesName)
	}
	// Check for required key usage.
	if !lo.Contains(csr.Usages(), string(certv1.UsageServerAuth)) {
		return fmt.Errorf("CSR does not contain usage %s", certv1.UsageServerAuth)
	}
	// Check for not permitted key usages.
	permitted := []string{string(certv1.UsageKeyEncipherment), string(certv1.UsageDigitalSignature), string(certv1.UsageServerAuth)}
	if notPermitted := lo.Without(csr.Usages(), permitted...); len(notPermitted) > 0 {
		return fmt.Errorf("CSR contains not permitted usages: %s", strings.Join(notPermitted, ", "))
	}
	x509CSR := csr.ParsedCertificateRequest()
	// Check no email addresses.
	if len(x509CSR.EmailAddresses) > 0 {
		return fmt.Errorf("CSR contains email addresses: %s", strings.Join(x509CSR.EmailAddresses, ", "))
	}
	// Check no URIs.
	if len(x509CSR.URIs) > 0 {
		slice := make([]string, len(x509CSR.URIs))
		for i, u := range x509CSR.URIs {
			slice[i] = u.String()
		}
		return fmt.Errorf("CSR contains URIs: %s", strings.Join(slice, ", "))
	}

	// Check with actual node the last to avoid unnecessary API calls.
	nodeName := strings.TrimPrefix(x509CSR.Subject.CommonName, "system:node:")
	node, err := m.clientset.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("getting node %s: %w", nodeName, err)
	}
	if len(x509CSR.IPAddresses) > 0 {
		for _, ip := range x509CSR.IPAddresses {
			if !lo.ContainsBy(node.Status.Addresses, func(addr corev1.NodeAddress) bool {
				return (addr.Type == corev1.NodeInternalIP || addr.Type == corev1.NodeExternalIP) && addr.Address == ip.String()
			}) {
				return fmt.Errorf("CSR contains IP address %s not in node %s", ip.String(), nodeName)
			}
		}
	}
	if len(x509CSR.DNSNames) > 0 {
		for _, dns := range x509CSR.DNSNames {
			if !lo.ContainsBy(node.Status.Addresses, func(addr corev1.NodeAddress) bool {
				if (addr.Type == corev1.NodeInternalDNS || addr.Type == corev1.NodeExternalDNS) && addr.Address == dns {
					return true
				}
				if addr.Type == corev1.NodeHostName && addr.Address == node.Name && addr.Address == dns {
					return true
				}
				return false
			}) {
				return fmt.Errorf("CSR contains DNS name %s not in node %s", dns, nodeName)
			}
		}
	}
	return nil
}

func (m *ApprovalManager) stopAutoApproveForCastAINodes() {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.cancelAutoApprove == nil {
		return
	}

	m.log.Info("stopping auto approve CSRs for managed by CAST AI nodes")
	m.cancelAutoApprove()
	m.cancelAutoApprove = nil
}

func (h *ApprovalManager) addInProgress(certName, signerName string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.inProgress == nil {
		h.inProgress = make(map[string]struct{})
	}
	key := createKey(certName, signerName)
	_, ok := h.inProgress[key]
	if ok {
		return false
	}
	h.inProgress[key] = struct{}{}
	return true
}

func (h *ApprovalManager) removeInProgress(certName, signerName string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	delete(h.inProgress, createKey(certName, signerName))
}

func createKey(certName, signerName string) string {
	return fmt.Sprintf("%s-%s", certName, signerName)
}
