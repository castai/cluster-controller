package actions

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes"

	"github.com/castai/cluster-controller/castai"
	"github.com/castai/cluster-controller/csr"
)

func newApproveCSRHandler(log logrus.FieldLogger, clientset kubernetes.Interface) ActionHandler {
	return &approveCSRHandler{
		log:                    log,
		clientset:              clientset,
		csrFetchInterval:       5 * time.Second,
		initialCSRFetchTimeout: 5 * time.Minute,
		maxRetries:             10,
		retryAfter:             1 * time.Second,
	}
}

type approveCSRHandler struct {
	log                    logrus.FieldLogger
	clientset              kubernetes.Interface
	csrFetchInterval       time.Duration
	initialCSRFetchTimeout time.Duration
	maxRetries             uint64
	retryAfter             time.Duration
}

func (h *approveCSRHandler) Handle(ctx context.Context, data interface{}) error {
	req, ok := data.(*castai.ActionApproveCSR)
	if !ok {
		return fmt.Errorf("unexpected type %T for approve csr handler", data)
	}

	log := h.log.WithField("node_name", req.NodeName)

	b := backoff.WithContext(
		newApproveCSRExponentialBackoff(),
		ctx,
	)
	return backoff.RetryNotify(func() error {
		return h.handle(ctx, log, req)
	}, b, func(err error, duration time.Duration) {
		if err != nil {
			log.Warnf("csr approval failed, will retry: %v", err)
		}
	})
}

func (h *approveCSRHandler) handle(ctx context.Context, log logrus.FieldLogger, req *castai.ActionApproveCSR) error {
	// First get original csr which is created by kubelet.
	log.Debug("getting initial csr")
	cert, err := h.getInitialNodeCSR(ctx, req.NodeName)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return backoff.Permanent(fmt.Errorf("getting initial csr: %w", err))
		}
		return fmt.Errorf("getting initial csr: %w", err)
	}

	if cert.Approved() {
		log.Debug("initial csr is already approved")
		return nil
	}

	// Since this new csr may be denied we need to delete it.
	log.Debug("deleting old csr")
	if err := csr.DeleteCertificate(ctx, h.clientset, cert); err != nil {
		return fmt.Errorf("deleting csr: %w", err)
	}

	// Create new csr with the same request data as original csr.
	log.Debug("requesting new csr")
	cert, err = csr.RequestCertificate(
		ctx,
		h.clientset,
		cert,
	)
	if err != nil {
		return fmt.Errorf("requesting new csr: %w", err)
	}

	// Approve new csr.
	log.Debug("approving new csr")
	resp, err := csr.ApproveCertificate(ctx, h.clientset, cert)
	if err != nil {
		return fmt.Errorf("approving csr: %w", err)
	}
	if resp.Approved() {
		return nil
	}
	return errors.New("certificate signing request was not approved")
}

func (h *approveCSRHandler) getInitialNodeCSR(ctx context.Context, nodeName string) (*csr.Certificate, error) {
	csrFetchCtx, cancel := context.WithTimeout(ctx, h.initialCSRFetchTimeout)
	defer cancel()

	for {
		select {
		case <-csrFetchCtx.Done():
			return nil, csrFetchCtx.Err()
		case <-time.After(h.csrFetchInterval):
			cert, err := csr.GetCertificateByNodeName(ctx, h.clientset, nodeName)
			if err != nil && !errors.Is(err, csr.ErrNodeCertificateNotFound) {
				return nil, err
			}
			if cert != nil {
				return cert, nil
			}
		}
	}
}

func newApproveCSRExponentialBackoff() *backoff.ExponentialBackOff {
	b := backoff.NewExponentialBackOff()
	b.Multiplier = 2
	b.MaxElapsedTime = 4 * time.Minute
	b.Reset()
	return b
}
