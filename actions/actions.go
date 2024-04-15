//go:generate mockgen -destination ./mock_actions/helm_client_mock.go github.com/castai/cluster-controller/actions HelmClient
//go:generate mockgen -destination ./mock_actions/client_mock.go github.com/castai/cluster-controller/actions Client
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

	"github.com/cenkalti/backoff/v4"
	"github.com/sirupsen/logrus"
	"helm.sh/helm/v3/pkg/release"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/castai/cluster-controller/actions/types"
	"github.com/castai/cluster-controller/health"
	"github.com/castai/cluster-controller/helm"
)

const (
	// actionIDLogField is the log field name for action ID.
	// This field is used in backend to detect actions ID in logs.
	actionIDLogField                                                 = "id"
	labelNodeID                                                      = "provisioner.cast.ai/node-id"
	actionCheckNodeStatus_READY   types.ActionCheckNodeStatus_Status = "NodeStatus_READY"
	actionCheckNodeStatus_DELETED types.ActionCheckNodeStatus_Status = "NodeStatus_DELETED"
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
	Handle(ctx context.Context, action *types.ClusterAction) error
}

type Client interface {
	GetActions(ctx context.Context, k8sVersion string) ([]*types.ClusterAction, error)
	AckAction(ctx context.Context, actionID string, req *types.AckClusterActionRequest) error
	SendAKSInitData(ctx context.Context, req *types.AKSInitDataRequest) error
}

type HelmClient interface {
	Install(ctx context.Context, opts helm.InstallOptions) (*release.Release, error)
	Uninstall(opts helm.UninstallOptions) (*release.UninstallReleaseResponse, error)
	Upgrade(ctx context.Context, opts helm.UpgradeOptions) (*release.Release, error)
	Rollback(opts helm.RollbackOptions) error
	GetRelease(opts helm.GetReleaseOptions) (*release.Release, error)
}

func NewService(
	log logrus.FieldLogger,
	cfg Config,
	k8sVersion string,
	clientset *kubernetes.Clientset,
	dynamicClient dynamic.Interface,
	client Client,
	helmClient HelmClient,
	healthCheck *health.HealthzProvider,
) Service {
	return &service{
		log:            log,
		cfg:            cfg,
		k8sVersion:     k8sVersion,
		client:         client,
		startedActions: map[string]struct{}{},
		actionHandlers: map[reflect.Type]ActionHandler{
			reflect.TypeOf(&types.ActionDeleteNode{}):        newDeleteNodeHandler(log, clientset),
			reflect.TypeOf(&types.ActionDrainNode{}):         newDrainNodeHandler(log, clientset, cfg.Namespace),
			reflect.TypeOf(&types.ActionPatchNode{}):         newPatchNodeHandler(log, clientset),
			reflect.TypeOf(&types.ActionCreateEvent{}):       newCreateEventHandler(log, clientset),
			reflect.TypeOf(&types.ActionApproveCSR{}):        newApproveCSRHandler(log, clientset),
			reflect.TypeOf(&types.ActionChartUpsert{}):       newChartUpsertHandler(log, helmClient),
			reflect.TypeOf(&types.ActionChartUninstall{}):    newChartUninstallHandler(log, helmClient),
			reflect.TypeOf(&types.ActionChartRollback{}):     newChartRollbackHandler(log, helmClient, cfg.Version),
			reflect.TypeOf(&types.ActionDisconnectCluster{}): newDisconnectClusterHandler(log, clientset),
			reflect.TypeOf(&types.ActionSendAKSInitData{}):   newSendAKSInitDataHandler(log, client),
			reflect.TypeOf(&types.ActionCheckNodeDeleted{}):  newCheckNodeDeletedHandler(log, clientset),
			reflect.TypeOf(&types.ActionCheckNodeStatus{}):   newCheckNodeStatusHandler(log, clientset),
			reflect.TypeOf(&types.ActionPatch{}):             newPatchHandler(log, dynamicClient),
			reflect.TypeOf(&types.ActionCreate{}):            newCreateHandler(log, dynamicClient),
			reflect.TypeOf(&types.ActionDelete{}):            newDeleteHandler(log, dynamicClient),
		},
		healthCheck: healthCheck,
	}
}

type service struct {
	log    logrus.FieldLogger
	cfg    Config
	client Client

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
		actions   []*types.ClusterAction
		err       error
		iteration int
	)

	b := backoff.WithContext(backoff.WithMaxRetries(backoff.NewConstantBackOff(5*time.Second), 3), ctx)
	errR := backoff.Retry(func() error {
		iteration++
		actions, err = s.client.GetActions(ctx, s.k8sVersion)
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

	s.log.WithFields(logrus.Fields{"n": strconv.Itoa(len(actions))}).Infof("received in %s", pollDuration)
	s.handleActions(ctx, actions)
	return nil
}

func (s *service) handleActions(ctx context.Context, actions []*types.ClusterAction) {
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

func (s *service) handleAction(ctx context.Context, action *types.ClusterAction) (err error) {
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

func (s *service) ackAction(ctx context.Context, action *types.ClusterAction, handleErr error) error {
	actionType := reflect.TypeOf(action.Data())
	s.log.WithFields(logrus.Fields{
		actionIDLogField: action.ID,
		"type":           actionType.String(),
	}).Info("ack action")

	return backoff.RetryNotify(func() error {
		ctx, cancel := context.WithTimeout(ctx, s.cfg.AckTimeout)
		defer cancel()
		return s.client.AckAction(ctx, action.ID, &types.AckClusterActionRequest{
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
