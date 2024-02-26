package actions

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime/debug"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/sirupsen/logrus"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/castai/cluster-controller/castai"
	"github.com/castai/cluster-controller/health"
	"github.com/castai/cluster-controller/helm"
)

func newUnexpectedTypeErr(value interface{}, expectedType interface{}) error {
	return fmt.Errorf("unexpected type %T, expected %T", value, expectedType)
}

type Config struct {
	PollWaitInterval time.Duration // How long to wait unit next long polling request.
	PollTimeout      time.Duration // hard timeout. Normally server should return empty result before this timeout.
	AckTimeout       time.Duration // How long to wait for ack request to complete.
	AckRetriesCount  int           // Ack retry count.
	AckRetryWait     time.Duration // How long to wait before next ack retry request.
	ClusterID        string
	Version          string
	Namespace        string
}

type Service interface {
	Run(ctx context.Context)
}

type ActionHandler interface {
	Handle(ctx context.Context, action *castai.ClusterAction) error
}

func NewService(
	log logrus.FieldLogger,
	cfg Config,
	k8sVersion string,
	clientset *kubernetes.Clientset,
	dynamicClient dynamic.Interface,
	castaiClient castai.Client,
	helmClient helm.Client,
	healthCheck *health.HealthzProvider,
) Service {
	return &service{
		log:            log,
		cfg:            cfg,
		k8sVersion:     k8sVersion,
		castAIClient:   castaiClient,
		startedActions: map[string]struct{}{},
		actionHandlers: map[reflect.Type]ActionHandler{
			reflect.TypeOf(&castai.ActionDeleteNode{}):        newDeleteNodeHandler(log, clientset),
			reflect.TypeOf(&castai.ActionDrainNode{}):         newDrainNodeHandler(log, clientset, cfg.Namespace),
			reflect.TypeOf(&castai.ActionPatchNode{}):         newPatchNodeHandler(log, clientset),
			reflect.TypeOf(&castai.ActionCreateEvent{}):       newCreateEventHandler(log, clientset),
			reflect.TypeOf(&castai.ActionApproveCSR{}):        newApproveCSRHandler(log, clientset),
			reflect.TypeOf(&castai.ActionChartUpsert{}):       newChartUpsertHandler(log, helmClient),
			reflect.TypeOf(&castai.ActionChartUninstall{}):    newChartUninstallHandler(log, helmClient),
			reflect.TypeOf(&castai.ActionChartRollback{}):     newChartRollbackHandler(log, helmClient, cfg.Version),
			reflect.TypeOf(&castai.ActionDisconnectCluster{}): newDisconnectClusterHandler(log, clientset),
			reflect.TypeOf(&castai.ActionSendAKSInitData{}):   newSendAKSInitDataHandler(log, castaiClient),
			reflect.TypeOf(&castai.ActionCheckNodeDeleted{}):  newCheckNodeDeletedHandler(log, clientset),
			reflect.TypeOf(&castai.ActionCheckNodeStatus{}):   newCheckNodeStatusHandler(log, clientset),
			reflect.TypeOf(&castai.ActionPatch{}):             newPatchHandler(log, dynamicClient),
			reflect.TypeOf(&castai.ActionCreate{}):            newCreateHandler(log, dynamicClient),
			reflect.TypeOf(&castai.ActionDelete{}):            newDeleteHandler(log, dynamicClient),
		},
		healthCheck: healthCheck,
	}
}

type service struct {
	log          logrus.FieldLogger
	cfg          Config
	castAIClient castai.Client

	k8sVersion string

	actionHandlers map[reflect.Type]ActionHandler

	startedActionsWg sync.WaitGroup
	startedActions   map[string]struct{}
	startedActionsMu sync.Mutex
	healthCheck      *health.HealthzProvider
}

func (s *service) Run(ctx context.Context) {
	s.healthCheck.Initializing()
	for {
		select {
		case <-time.After(s.cfg.PollWaitInterval):
			err := s.doWork(ctx)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					s.log.Info("service stopped")
					return
				}

				s.log.Errorf("cycle failed: %v", err)
				continue
			}
		case <-ctx.Done():
			s.log.Info("service stopped")
			return
		}
	}
}

func (s *service) doWork(ctx context.Context) error {
	s.log.Info("polling actions")
	start := time.Now()
	var (
		actions   []*castai.ClusterAction
		err       error
		iteration int
	)

	b := backoff.WithContext(backoff.WithMaxRetries(backoff.NewConstantBackOff(5*time.Second), 3), ctx)
	errR := backoff.Retry(func() error {
		iteration++
		actions, err = s.castAIClient.GetActions(ctx, s.k8sVersion)
		if err != nil {
			s.log.Errorf("polling actions: get action request failed: iteration: %v %v", iteration, err)
			return err
		}
		return nil
	}, b)
	if errR != nil {
		return fmt.Errorf("polling actions: %w", err)
	}

	s.healthCheck.ActionPoll()
	pollDuration := time.Since(start)
	if len(actions) == 0 {
		s.log.Infof("no actions returned in %s", pollDuration)
		return nil
	}

	s.log.WithFields(logrus.Fields{"n": len(actions)}).Infof("received in %s", pollDuration)
	s.handleActions(ctx, actions)
	return nil
}

func (s *service) handleActions(ctx context.Context, actions []*castai.ClusterAction) {
	for _, action := range actions {
		if !s.startProcessing(action.ID) {
			continue
		}

		go func(action *castai.ClusterAction) {
			defer s.finishProcessing(action.ID)

			var err error
			handleErr := s.handleAction(ctx, action)
			if errors.Is(handleErr, context.Canceled) {
				// Action should be handled again on context canceled errors.
				return
			}
			ackErr := s.ackAction(ctx, action, handleErr)
			if handleErr != nil {
				err = handleErr
			}
			if ackErr != nil {
				err = fmt.Errorf("%v:%w", err, ackErr)
			}
			if err != nil {
				s.log.WithFields(logrus.Fields{
					"id":    action.ID,
					"error": err,
				}).Error("handle actions")
			}
		}(action)
	}
}

func (s *service) finishProcessing(actionID string) {
	s.startedActionsMu.Lock()
	defer s.startedActionsMu.Unlock()

	s.startedActionsWg.Done()
	delete(s.startedActions, actionID)
}

func (s *service) startProcessing(actionID string) bool {
	s.startedActionsMu.Lock()
	defer s.startedActionsMu.Unlock()

	if _, ok := s.startedActions[actionID]; ok {
		return false
	}

	s.startedActionsWg.Add(1)
	s.startedActions[actionID] = struct{}{}
	return true
}

func (s *service) handleAction(ctx context.Context, action *castai.ClusterAction) (err error) {
	actionType := reflect.TypeOf(action.Data())

	defer func() {
		if rerr := recover(); rerr != nil {
			err = fmt.Errorf("panic: handling action %s: %s: %s", actionType, rerr, string(debug.Stack()))
		}
	}()

	s.log.WithFields(logrus.Fields{
		"id":   action.ID,
		"type": actionType,
	}).Info("handle action")
	handler, ok := s.actionHandlers[actionType]
	if !ok {
		return fmt.Errorf("handler not found for agent action=%s", actionType)
	}

	if err := handler.Handle(ctx, action); err != nil {
		return fmt.Errorf("handling action %v: %w", actionType, err)
	}
	return nil
}

func (s *service) ackAction(ctx context.Context, action *castai.ClusterAction, handleErr error) error {
	actionType := reflect.TypeOf(action.Data())
	s.log.WithFields(logrus.Fields{
		"id":   action.ID,
		"type": actionType,
	}).Info("ack action")

	return backoff.RetryNotify(func() error {
		ctx, cancel := context.WithTimeout(ctx, s.cfg.AckTimeout)
		defer cancel()
		return s.castAIClient.AckAction(ctx, action.ID, &castai.AckClusterActionRequest{
			Error: getHandlerError(handleErr),
		})
	}, backoff.WithContext(
		backoff.WithMaxRetries(
			backoff.NewConstantBackOff(s.cfg.AckRetryWait), uint64(s.cfg.AckRetriesCount),
		),
		ctx,
	), func(err error, duration time.Duration) {
		if err != nil {
			s.log.Debugf("ack failed, will retry: %v", err)
		}
	})
}

func getHandlerError(err error) *string {
	if err != nil {
		str := err.Error()
		return &str
	}
	return nil
}
