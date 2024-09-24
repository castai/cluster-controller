package actions

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"

	"github.com/castai/cluster-controller/castai"
	"github.com/castai/cluster-controller/csr"
	"github.com/castai/cluster-controller/waitext"
)

const (
	approveCSRTimeout = 4 * time.Minute
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
	cancelAutoApprove      context.CancelFunc
	m                      sync.Mutex // Used to make sure there is just one watcher running as it may be triggered from multiple CSR actions.
}

func (h *approveCSRHandler) Handle(ctx context.Context, action *castai.ClusterAction) error {
	req, ok := action.Data().(*castai.ActionApproveCSR)
	if !ok {
		return fmt.Errorf("unexpected type %T for approve csr handler", action.Data())
	}
	log := h.log.WithFields(logrus.Fields{
		"node_name":      req.NodeName,
		"node_id":        req.NodeID,
		"type":           reflect.TypeOf(action.Data().(*castai.ActionApproveCSR)).String(),
		actionIDLogField: action.ID,
	})

	if req.AllowAutoApprove != nil {
		// CSR action may be used only to instruct whether to start / stop watcher responsible for auto-approving; in
		// this case, there is nothing more to do.
		if req.NodeName == "" {
			return nil
		}
	}

	cert, err := h.getInitialNodeCSR(ctx, log, req.NodeName)
	if err != nil {
		return fmt.Errorf("getting initial csr: %w", err)
	}

	if cert.Approved() {
		log.Debug("csr is already approved")
		return nil
	}

	return h.handleWithRetry(ctx, log, cert)
}

func (h *approveCSRHandler) handleWithRetry(ctx context.Context, log *logrus.Entry, cert *csr.Certificate) error {
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

func (h *approveCSRHandler) handle(ctx context.Context, log logrus.FieldLogger, cert *csr.Certificate) (reterr error) {
	// Since this new csr may be denied we need to delete it.
	log.Debug("deleting old csr")
	if err := cert.DeleteCertificate(ctx, h.clientset); err != nil {
		return fmt.Errorf("deleting csr: %w", err)
	}

	// Create a new CSR with the same request data as the original one.
	log.Debug("requesting new csr")
	newCert, err := cert.NewCSR(ctx, h.clientset)
	if err != nil {
		return fmt.Errorf("requesting new csr: %w", err)
	}

	// Approve new csr.
	log.Debug("approving new csr")
	resp, err := newCert.ApproveCertificate(ctx, h.clientset)
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

	b := waitext.DefaultExponentialBackoff()
	err = waitext.Retry(
		ctx,
		b,
		3,
		func(ctx context.Context) (bool, error) {
			cert, err = poll()
			if errors.Is(err, context.DeadlineExceeded) {
				return false, err
			}
			return true, err
		},
		func(err error) {
			log.Warnf("getting initial csr, will retry: %v", err)
		},
	)

	return cert, err
}

func newApproveCSRExponentialBackoff() wait.Backoff {
	b := waitext.DefaultExponentialBackoff()
	b.Factor = 2
	return b
}
