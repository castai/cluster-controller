package controller

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime/debug"
	"strconv"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/castai/cluster-controller/health"
	"github.com/castai/cluster-controller/internal/actions"
	"github.com/castai/cluster-controller/internal/castai"
	"github.com/castai/cluster-controller/internal/helm"
	"github.com/castai/cluster-controller/internal/waitext"
)

type Config struct {
	PollWaitInterval     time.Duration // How long to wait unit next long polling request.
	PollTimeout          time.Duration // hard timeout. Normally server should return empty result before this timeout.
	AckTimeout           time.Duration // How long to wait for ack request to complete.
	AckRetriesCount      int           // Ack retry count.
	AckRetryWait         time.Duration // How long to wait before next ack retry request.
	ClusterID            string
	Version              string
	Namespace            string
	MaxActionsInProgress int
}

func NewService(
	log logrus.FieldLogger,
	cfg Config,
	k8sVersion string,
	clientset *kubernetes.Clientset,
	dynamicClient dynamic.Interface,
	castaiClient castai.CastAIClient,
	helmClient helm.Client,
	healthCheck *health.HealthzProvider,
) *Controller {
	return &Controller{
		log:            log,
		cfg:            cfg,
		k8sVersion:     k8sVersion,
		castAIClient:   castaiClient,
		startedActions: map[string]struct{}{},
		actionHandlers: map[reflect.Type]actions.ActionHandler{
			reflect.TypeOf(&castai.ActionDeleteNode{}):        actions.NewDeleteNodeHandler(log, clientset),
			reflect.TypeOf(&castai.ActionDrainNode{}):         actions.NewDrainNodeHandler(log, clientset, cfg.Namespace),
			reflect.TypeOf(&castai.ActionPatchNode{}):         actions.NewPatchNodeHandler(log, clientset),
			reflect.TypeOf(&castai.ActionCreateEvent{}):       actions.NewCreateEventHandler(log, clientset),
			reflect.TypeOf(&castai.ActionApproveCSR{}):        actions.NewApproveCSRHandler(log, clientset),
			reflect.TypeOf(&castai.ActionChartUpsert{}):       actions.NewChartUpsertHandler(log, helmClient),
			reflect.TypeOf(&castai.ActionChartUninstall{}):    actions.NewChartUninstallHandler(log, helmClient),
			reflect.TypeOf(&castai.ActionChartRollback{}):     actions.NewChartRollbackHandler(log, helmClient, cfg.Version),
			reflect.TypeOf(&castai.ActionDisconnectCluster{}): actions.NewDisconnectClusterHandler(log, clientset),
			reflect.TypeOf(&castai.ActionSendAKSInitData{}):   actions.NewSendAKSInitDataHandler(log, castaiClient),
			reflect.TypeOf(&castai.ActionCheckNodeDeleted{}):  actions.NewCheckNodeDeletedHandler(log, clientset),
			reflect.TypeOf(&castai.ActionCheckNodeStatus{}):   actions.NewCheckNodeStatusHandler(log, clientset),
			reflect.TypeOf(&castai.ActionEvictPod{}):          actions.NewEvictPodHandler(log, clientset),
			reflect.TypeOf(&castai.ActionPatch{}):             actions.NewPatchHandler(log, dynamicClient),
			reflect.TypeOf(&castai.ActionCreate{}):            actions.NewCreateHandler(log, dynamicClient),
			reflect.TypeOf(&castai.ActionDelete{}):            actions.NewDeleteHandler(log, dynamicClient),
		},
		healthCheck: healthCheck,
	}
}

type Controller struct {
	log          logrus.FieldLogger
	cfg          Config
	castAIClient castai.CastAIClient

	k8sVersion string

	actionHandlers map[reflect.Type]actions.ActionHandler

	startedActionsWg sync.WaitGroup
	startedActions   map[string]struct{}
	startedActionsMu sync.Mutex
	healthCheck      *health.HealthzProvider
}

func (s *Controller) Run(ctx context.Context) {
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

func (s *Controller) doWork(ctx context.Context) error {
	s.log.Info("polling actions")
	start := time.Now()
	var (
		actions   []*castai.ClusterAction
		err       error
		iteration int
	)

	boff := waitext.NewConstantBackoff(5 * time.Second)

	errR := waitext.Retry(ctx, boff, 3, func(ctx context.Context) (bool, error) {
		iteration++
		actions, err = s.castAIClient.GetActions(ctx, s.k8sVersion)
		if err != nil {
			return true, err
		}
		return false, nil
	}, func(err error) {
		s.log.Errorf("polling actions: get action request failed: iteration: %v %v", iteration, err)
	})

	if errR != nil {
		return fmt.Errorf("polling actions: %w", err)
	}

	s.healthCheck.ActionPoll()
	pollDuration := time.Since(start)
	if len(actions) == 0 {
		s.log.Infof("no actions returned in %s", pollDuration)
		return nil
	}

	s.log.WithFields(logrus.Fields{"n": strconv.Itoa(len(actions))}).Infof("received in %s", pollDuration)
	s.handleActions(ctx, actions)
	return nil
}

func (s *Controller) handleActions(ctx context.Context, clusterActions []*castai.ClusterAction) {
	for _, action := range clusterActions {
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
				err = fmt.Errorf("%v:%w", err, ackErr) // nolint:errorlint
			}
			if err != nil {
				s.log.WithFields(logrus.Fields{
					actions.ActionIDLogField: action.ID,
					"error":                  err.Error(),
				}).Error("handle actions")
			}
		}(action)
	}
}

func (s *Controller) finishProcessing(actionID string) {
	s.startedActionsMu.Lock()
	defer s.startedActionsMu.Unlock()

	s.startedActionsWg.Done()
	delete(s.startedActions, actionID)
}

func (s *Controller) startProcessing(actionID string) bool {
	s.startedActionsMu.Lock()
	defer s.startedActionsMu.Unlock()

	if _, ok := s.startedActions[actionID]; ok {
		return false
	}

	if inProgress := len(s.startedActions); inProgress >= s.cfg.MaxActionsInProgress {
		s.log.Warnf("too many actions in progress %d/%d", inProgress, s.cfg.MaxActionsInProgress)
		return false
	}

	s.startedActionsWg.Add(1)
	s.startedActions[actionID] = struct{}{}
	return true
}

func (s *Controller) handleAction(ctx context.Context, action *castai.ClusterAction) (err error) {
	actionType := reflect.TypeOf(action.Data())

	defer func() {
		if rerr := recover(); rerr != nil {
			err = fmt.Errorf("panic: handling action %s: %s: %s", actionType, rerr, string(debug.Stack()))
		}
	}()

	s.log.WithFields(logrus.Fields{
		actions.ActionIDLogField: action.ID,
		"type":                   actionType.String(),
	}).Info("handle action")
	handler, ok := s.actionHandlers[actionType]
	if !ok {
		return fmt.Errorf("handler not found for action=%s", actionType)
	}

	if err := handler.Handle(ctx, action); err != nil {
		return fmt.Errorf("handling action %v: %w", actionType, err)
	}
	return nil
}

func (s *Controller) ackAction(ctx context.Context, action *castai.ClusterAction, handleErr error) error {
	actionType := reflect.TypeOf(action.Data())
	s.log.WithFields(logrus.Fields{
		actions.ActionIDLogField: action.ID,
		"type":                   actionType.String(),
	}).Info("ack action")

	boff := waitext.NewConstantBackoff(s.cfg.AckRetryWait)

	return waitext.Retry(ctx, boff, s.cfg.AckRetriesCount, func(ctx context.Context) (bool, error) {
		ctx, cancel := context.WithTimeout(ctx, s.cfg.AckTimeout)
		defer cancel()
		return true, s.castAIClient.AckAction(ctx, action.ID, &castai.AckClusterActionRequest{
			Error: getHandlerError(handleErr),
		})
	}, func(err error) {
		s.log.Debugf("ack failed, will retry: %v", err)
	})
}

func getHandlerError(err error) *string {
	if err != nil {
		str := err.Error()
		return &str
	}
	return nil
}

func (s *Controller) Close() error {
	return s.actionHandlers[reflect.TypeOf(&castai.ActionCreateEvent{})].(*actions.CreateEventHandler).Close()
}
