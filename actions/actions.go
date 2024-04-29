// Package actions polls, handles and acknowledges actions from mothership for a given cluster.
//
//go:generate mockgen -destination ./mock/client.go . Client
package actions

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
	"github.com/castai/cluster-controller/helm"
	"github.com/castai/cluster-controller/types"
	"github.com/castai/cluster-controller/waitext"
)

const (
	// actionIDLogField is the log field name for action ID.
	// This field is used in backend to detect actions ID in logs.
	actionIDLogField = "id"
	labelNodeID      = "provisioner.cast.ai/node-id"
)

func newUnexpectedTypeErr(value interface{}, expectedType interface{}) error {
	return fmt.Errorf("unexpected type %T, expected %T", value, expectedType)
}

// Config contains parameters to modify actions handling frequency and values required to poll/ack actions.
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

// Client abstracts communication means.
type Client interface {
	GetActions(ctx context.Context, k8sVersion string) ([]*types.ClusterAction, error)
	AckAction(ctx context.Context, actionID string, errMessage *string) error
	SendAKSInitData(ctx context.Context, cloudConfigBase64, protectedSettingsBase64, architecture string) error
}

type actionHandler interface {
	Handle(ctx context.Context, action *types.ClusterAction) error
}

// NewService returns new Service that can continuously handle actions once started.
func NewService(
	log logrus.FieldLogger,
	cfg Config,
	k8sVersion string,
	clientset *kubernetes.Clientset,
	dynamicClient dynamic.Interface,
	castaiClient Client,
	helmClient helm.Client,
	healthCheck *health.HealthzProvider,
) *Service {
	return &Service{
		log:            log,
		cfg:            cfg,
		k8sVersion:     k8sVersion,
		castAIClient:   castaiClient,
		startedActions: map[string]struct{}{},
		actionHandlers: map[reflect.Type]actionHandler{
			reflect.TypeOf(&types.ActionDeleteNode{}):        newDeleteNodeHandler(log, clientset),
			reflect.TypeOf(&types.ActionDrainNode{}):         newDrainNodeHandler(log, clientset, cfg.Namespace),
			reflect.TypeOf(&types.ActionPatchNode{}):         newPatchNodeHandler(log, clientset),
			reflect.TypeOf(&types.ActionCreateEvent{}):       newCreateEventHandler(log, clientset),
			reflect.TypeOf(&types.ActionApproveCSR{}):        newApproveCSRHandler(log, clientset),
			reflect.TypeOf(&types.ActionChartUpsert{}):       newChartUpsertHandler(log, helmClient),
			reflect.TypeOf(&types.ActionChartUninstall{}):    newChartUninstallHandler(log, helmClient),
			reflect.TypeOf(&types.ActionChartRollback{}):     newChartRollbackHandler(log, helmClient, cfg.Version),
			reflect.TypeOf(&types.ActionDisconnectCluster{}): newDisconnectClusterHandler(log, clientset),
			reflect.TypeOf(&types.ActionSendAKSInitData{}):   newSendAKSInitDataHandler(log, castaiClient),
			reflect.TypeOf(&types.ActionCheckNodeDeleted{}):  newCheckNodeDeletedHandler(log, clientset),
			reflect.TypeOf(&types.ActionCheckNodeStatus{}):   newCheckNodeStatusHandler(log, clientset),
			reflect.TypeOf(&types.ActionPatch{}):             newPatchHandler(log, dynamicClient),
			reflect.TypeOf(&types.ActionCreate{}):            newCreateHandler(log, dynamicClient),
			reflect.TypeOf(&types.ActionDelete{}):            newDeleteHandler(log, dynamicClient),
		},
		healthCheck: healthCheck,
	}
}

// Service can continuously poll and handle actions.
type Service struct {
	log          logrus.FieldLogger
	cfg          Config
	castAIClient Client

	k8sVersion string

	actionHandlers map[reflect.Type]actionHandler

	startedActionsWg sync.WaitGroup
	startedActions   map[string]struct{}
	startedActionsMu sync.Mutex
	healthCheck      *health.HealthzProvider
}

// Run starts polling and handling actions.
func (s *Service) Run(ctx context.Context) {
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

func (s *Service) doWork(ctx context.Context) error {
	s.log.Info("polling actions")
	start := time.Now()
	var (
		actions   []*types.ClusterAction
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

func (s *Service) handleActions(ctx context.Context, actions []*types.ClusterAction) {
	for _, action := range actions {
		if !s.startProcessing(action.ID) {
			continue
		}

		go func(action *types.ClusterAction) {
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
					actionIDLogField: action.ID,
					"error":          err.Error(),
				}).Error("handle actions")
			}
		}(action)
	}
}

func (s *Service) finishProcessing(actionID string) {
	s.startedActionsMu.Lock()
	defer s.startedActionsMu.Unlock()

	s.startedActionsWg.Done()
	delete(s.startedActions, actionID)
}

func (s *Service) startProcessing(actionID string) bool {
	s.startedActionsMu.Lock()
	defer s.startedActionsMu.Unlock()

	if _, ok := s.startedActions[actionID]; ok {
		return false
	}

	s.startedActionsWg.Add(1)
	s.startedActions[actionID] = struct{}{}
	return true
}

func (s *Service) handleAction(ctx context.Context, action *types.ClusterAction) (err error) {
	actionType := reflect.TypeOf(action.Data())

	defer func() {
		if rerr := recover(); rerr != nil {
			err = fmt.Errorf("panic: handling action %s: %s: %s", actionType, rerr, string(debug.Stack()))
		}
	}()

	s.log.WithFields(logrus.Fields{
		actionIDLogField: action.ID,
		"type":           actionType.String(),
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

func (s *Service) ackAction(ctx context.Context, action *types.ClusterAction, handleErr error) error {
	actionType := reflect.TypeOf(action.Data())
	s.log.WithFields(logrus.Fields{
		actionIDLogField: action.ID,
		"type":           actionType.String(),
	}).Info("ack action")

	boff := waitext.NewConstantBackoff(s.cfg.AckRetryWait)

	return waitext.Retry(ctx, boff, s.cfg.AckRetriesCount, func(ctx context.Context) (bool, error) {
		ctx, cancel := context.WithTimeout(ctx, s.cfg.AckTimeout)
		defer cancel()
		return true, s.castAIClient.AckAction(ctx, action.ID, getHandlerError(handleErr))
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
