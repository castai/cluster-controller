package csr

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"

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

func (h *ApprovalManager) Start(ctx context.Context) {
	go h.runAutoApproveForCastAINodes(ctx)
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

	return errors.New("certificate signing request was not approved")
}

func (h *ApprovalManager) runAutoApproveForCastAINodes(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	if !h.startAutoApprove(cancel) {
		return // already running.
	}
	defer h.stopAutoApproveForCastAINodes()

	log := h.log.WithField("RunAutoApprove", "auto-approve-csr")
	c := make(chan *Certificate, 1)
	go WatchCastAINodeCSRs(ctx, log, h.clientset, c)

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
			if !h.addInProgress(cert.Name) {
				continue
			}
			go func(cert *Certificate) {
				defer h.removeInProgress(cert.Name)

				log := log.WithField("node_name", cert.Name)
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

func (h *ApprovalManager) addInProgress(nodeName string) bool {
	h.m.Lock()
	defer h.m.Unlock()
	if h.inProgress == nil {
		h.inProgress = make(map[string]struct{})
	}
	_, ok := h.inProgress[nodeName]
	if ok {
		return false
	}
	h.inProgress[nodeName] = struct{}{}
	return true
}

func (h *ApprovalManager) removeInProgress(nodeName string) {
	h.m.Lock()
	defer h.m.Unlock()

	delete(h.inProgress, nodeName)
}
