package actions

import (
	"context"
	"errors"
	"fmt"
	"reflect"
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
		initialCSRFetchTimeout: 5 * time.Minute,
		csrFetchInterval:       5 * time.Second,
	}
}

type approveCSRHandler struct {
	log                    logrus.FieldLogger
	clientset              kubernetes.Interface
	initialCSRFetchTimeout time.Duration
	csrFetchInterval       time.Duration
}

func (h *approveCSRHandler) Handle(ctx context.Context, data interface{}, actionID string) error {
	req, ok := data.(*castai.ActionApproveCSR)
	if !ok {
		return fmt.Errorf("unexpected type %T for approve csr handler", data)
	}
	log := h.log.WithFields(logrus.Fields{
		"node_name": req.NodeName,
		"node_id":   req.NodeID,
		"type":      reflect.TypeOf(data.(*castai.ActionApproveCSR)).String(),
		"id":        actionID,
	})

	cert, err := h.getInitialNodeCSR(ctx, log, req.NodeName)
	if err != nil {
		return fmt.Errorf("getting initial csr: %w", err)
	}

	if cert.Approved() {
		log.Debug("csr is already approved")
		return nil
	}

	b := backoff.WithContext(
		newApproveCSRExponentialBackoff(),
		ctx,
	)
	return backoff.RetryNotify(func() error {
		return h.handle(ctx, log, cert)
	}, b, func(err error, duration time.Duration) {
		if err != nil {
			log.Warnf("csr approval failed, will retry: %v", err)
		}
	})
}

func (h *approveCSRHandler) handle(ctx context.Context, log logrus.FieldLogger, cert *csr.Certificate) (reterr error) {
	// Since this new csr may be denied we need to delete it.
	log.Debug("deleting old csr")
	if err := csr.DeleteCertificate(ctx, h.clientset, cert); err != nil {
		return fmt.Errorf("deleting csr: %w", err)
	}

	// Create new csr with the same request data as original csr.
	log.Debug("requesting new csr")
	newCert, err := csr.RequestCertificate(
		ctx,
		h.clientset,
		cert,
	)
	if err != nil {
		return fmt.Errorf("requesting new csr: %w", err)
	}

	// Approve new csr.
	log.Debug("approving new csr")
	resp, err := csr.ApproveCertificate(ctx, h.clientset, newCert)
	if err != nil {
		return fmt.Errorf("approving csr: %w", err)
	}
	if resp.Approved() {
		return nil
	}
	return errors.New("certificate signing request was not approved")
}

func (h *approveCSRHandler) getInitialNodeCSR(ctx context.Context, log logrus.FieldLogger, nodeName string) (*csr.Certificate, error) {
	log.Debug("getting initial csr")

	ctx, cancel := context.WithTimeout(ctx, h.initialCSRFetchTimeout)
	defer cancel()

	poll := func() (*csr.Certificate, error) {
		for {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
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

	var cert *csr.Certificate
	var err error

	logRetry := func(err error, _ time.Duration) {
		log.Warnf("getting initial csr, will retry: %v", err)
	}
	b := backoff.WithMaxRetries(backoff.NewExponentialBackOff(), 3)
	err = backoff.RetryNotify(func() error {
		cert, err = poll()
		if errors.Is(err, context.DeadlineExceeded) {
			return backoff.Permanent(err)
		}
		return err
	}, b, logRetry)

	return cert, err
}

func newApproveCSRExponentialBackoff() *backoff.ExponentialBackOff {
	b := backoff.NewExponentialBackOff()
	b.Multiplier = 2
	b.MaxElapsedTime = 4 * time.Minute
	b.Reset()
	return b
}
