package csr

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	certv1 "k8s.io/api/certificates/v1"
	certv1beta1 "k8s.io/api/certificates/v1beta1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	"github.com/castai/cluster-controller/internal/waitext"
)

const (
	approveCSRTimeout = 4 * time.Minute
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
	m          sync.Mutex          // Used to make sure there is just one watcher running.
}

func (h *ApprovalManager) Start(ctx context.Context) error {
	informerKubeletSignerFactory, csrInformerKubeletSigner, err := createInformer(
		ctx,
		h.clientset,
		getOptions(certv1.KubeAPIServerClientKubeletSignerName).FieldSelector,
		getOptions(certv1beta1.KubeAPIServerClientKubeletSignerName).FieldSelector)
	if err != nil {
		return fmt.Errorf("while creating informer for %v: %w", certv1.KubeAPIServerClientKubeletSignerName, err)
	}

	informerKubeletServingFactory, csrInformerKubeletServing, err := createInformer(
		ctx,
		h.clientset,
		getOptions(certv1.KubeletServingSignerName).FieldSelector,
		getOptions(certv1beta1.KubeletServingSignerName).FieldSelector)
	if err != nil {
		return fmt.Errorf("while creating informer for %v: %w", certv1.KubeletServingSignerName, err)
	}

	c := make(chan *Certificate, 1)

	handlerFuncs := cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			if err := processCSREvent(ctx, c, obj); err != nil {
				h.log.WithError(err).Warn("failed to process csr add event")
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
	if !h.startAutoApprove(cancel) {
		return nil
	}

	go startInformers(ctx, h.log, informerKubeletSignerFactory, informerKubeletServingFactory)
	go h.runAutoApproveForCastAINodes(ctx, c)

	return nil
}

func (h *ApprovalManager) Stop() {
	h.stopAutoApproveForCastAINodes()
}

func (h *ApprovalManager) handleWithRetry(ctx context.Context, log *logrus.Entry, cert *Certificate) error {
	ctx, cancel := context.WithTimeout(ctx, approveCSRTimeout)
	defer cancel()

	b := newApproveCSRExponentialBackoff()
	return waitext.Retry(
		ctx,
		b,
		waitext.Forever,
		func(ctx context.Context) (bool, error) {
			return true, h.handle(ctx, log, cert)
		},
		func(err error) {
			log.Warnf("csr approval failed, will retry: %v", err)
		},
	)
}

var errCSRNotApproved = errors.New("certificate signing request was not approved")

func (h *ApprovalManager) handle(ctx context.Context, log logrus.FieldLogger, cert *Certificate) (reterr error) {
	if cert.Approved() {
		return nil
	}
	log = log.WithField("csr_name", cert.Name)

	// Create a new CSR with the same request data as the original one,
	// since the old csr maybe denied.
	log.Info("requesting new csr")
	newCert, err := cert.NewCSR(ctx, h.clientset)
	if err != nil {
		return fmt.Errorf("requesting new csr: %w", err)
	}

	// Approve new csr.
	log.Info("approving new csr")
	resp, err := newCert.ApproveCSRCertificate(ctx, h.clientset)
	if err != nil {
		return fmt.Errorf("approving csr: %w", err)
	}
	if resp.Approved() {
		return nil
	}

	// clean original csr. should be the last step for having the possibility.
	// continue approving csr: old deleted-> restart-> node never join.
	log.Info("deleting old csr")
	if err := cert.DeleteCSR(ctx, h.clientset); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("deleting csr: %w", err)
		}
	}

	return errCSRNotApproved
}

func (h *ApprovalManager) runAutoApproveForCastAINodes(ctx context.Context, c <-chan *Certificate) {
	defer h.stopAutoApproveForCastAINodes()

	log := h.log.WithField("RunAutoApprove", "auto-approve-csr")

	for {
		select {
		case <-ctx.Done():
			log.WithError(ctx.Err()).Errorf("auto approve csr finished")
			return
		case cert := <-c:
			if cert == nil {
				continue
			}
			// prevent starting goroutine for the same node certificate
			if !h.addInProgress(cert.Name, cert.SignerName()) {
				continue
			}
			go func(cert *Certificate) {
				defer h.removeInProgress(cert.Name, cert.SignerName())

				log := log.WithFields(logrus.Fields{
					"csr_name":          cert.Name,
					"signer":            cert.SignerName(),
					"original_csr_name": cert.OriginalCSRName(),
				})
				log.Info("auto approving csr")
				err := h.handleWithRetry(ctx, log, cert)
				if err != nil {
					log.WithError(err).Errorf("failed to approve csr: %+v", cert)
				}
			}(cert)
		}
	}
}

func (h *ApprovalManager) startAutoApprove(cancelFunc context.CancelFunc) bool {
	h.m.Lock()
	defer h.m.Unlock()
	if h.cancelAutoApprove != nil {
		return false
	}

	h.log.Info("starting auto approve CSRs for managed by CAST AI nodes")
	h.cancelAutoApprove = cancelFunc

	return true
}

func (h *ApprovalManager) stopAutoApproveForCastAINodes() {
	h.m.Lock()
	defer h.m.Unlock()

	if h.cancelAutoApprove == nil {
		return
	}

	h.log.Info("stopping auto approve CSRs for managed by CAST AI nodes")
	h.cancelAutoApprove()
	h.cancelAutoApprove = nil
}

func newApproveCSRExponentialBackoff() wait.Backoff {
	b := waitext.DefaultExponentialBackoff()
	b.Factor = 2
	return b
}

func (h *ApprovalManager) addInProgress(certName, signerName string) bool {
	h.m.Lock()
	defer h.m.Unlock()
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
	h.m.Lock()
	defer h.m.Unlock()

	delete(h.inProgress, createKey(certName, signerName))
}

func createKey(certName, signerName string) string {
	return fmt.Sprintf("%s-%s", certName, signerName)
}
