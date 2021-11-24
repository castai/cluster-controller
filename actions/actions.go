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
)

type Config struct {
	PollInterval    time.Duration // How long to wait unit next long polling request.
	PollTimeout     time.Duration // hard timeout. Normally server should return empty result before this timeout.
	AckTimeout      time.Duration // How long to wait for ack request to complete.
	AckRetriesCount int           // Ack retry count.
	AckRetryWait    time.Duration // How long to wait before next ack retry request.
	ClusterID       string
}

type Service interface {
	Run(ctx context.Context)
}

type ActionHandler interface {
	Handle(ctx context.Context, data interface{}) error
}

func NewService(
	log logrus.FieldLogger,
	cfg Config,
	clientset *kubernetes.Clientset,
	castaiClient castai.Client,
) Service {
	return &service{
		log:          log,
		cfg:          cfg,
		castaiClient: castaiClient,
		actionHandlers: map[reflect.Type]ActionHandler{
			reflect.TypeOf(&castai.ActionDeleteNode{}):  newDeleteNodeHandler(log, clientset),
			reflect.TypeOf(&castai.ActionDrainNode{}):   newDrainNodeHandler(log, clientset),
			reflect.TypeOf(&castai.ActionPatchNode{}):   newPatchNodeHandler(log, clientset),
			reflect.TypeOf(&castai.ActionCreateEvent{}): newCreateEventHandler(log, clientset),
		},
	}
}

type service struct {
	log          logrus.FieldLogger
	cfg          Config
	castaiClient castai.Client

	actionHandlers map[reflect.Type]ActionHandler
}

func (s *service) Run(ctx context.Context) {
	for {
		select {
		case <-time.After(s.cfg.PollInterval):
			err := s.doWork(ctx)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					s.log.Debug("actions service stopped")
					return
				}

				s.log.Errorf("cycle failed: %v", err)
				continue
			}
		case <-ctx.Done():
			s.log.Debug("actions service stopped")
			return
		}
	}
}

func (s *service) doWork(ctx context.Context) error {
	s.log.Debug("polling actions")
	start := time.Now()
	actions, err := s.pollActions(ctx)
	if err != nil {
		return fmt.Errorf("polling actions: %w", err)
	}

	pollDuration := time.Now().Sub(start)
	if len(actions) == 0 {
		s.log.Debugf("no actions returned in %s", pollDuration)
		return nil
	}

	s.log.Debugf("received %d actions in %s", len(actions), pollDuration)
	if err := s.handleActions(ctx, actions); err != nil {
		return fmt.Errorf("handling actions: %w", err)
	}
	return nil
}

func (s *service) pollActions(ctx context.Context) ([]*castai.ClusterAction, error) {
	ctx, cancel := context.WithTimeout(ctx, s.cfg.PollTimeout)
	defer cancel()
	actions, err := s.castaiClient.GetActions(ctx)
	if err != nil {
		return nil, err
	}
	return actions, nil
}

func (s *service) handleActions(ctx context.Context, actions []*castai.ClusterAction) error {
	for _, action := range actions {
		var err error
		handleErr := s.handleAction(ctx, action)
		ackErr := s.ackAction(ctx, action, handleErr)
		if handleErr != nil {
			err = handleErr
		}
		if ackErr != nil {
			err = fmt.Errorf("%v:%w", err, ackErr)
		}
		if err != nil {
			return fmt.Errorf("action handling failed: %w", err)
		}
	}

	return nil
}

func (s *service) handleAction(ctx context.Context, action *castai.ClusterAction) (err error) {
	data := action.Data()
	actionType := reflect.TypeOf(data)
	s.log.Infof("handling action, id=%s, type=%s", action.ID, actionType)
	handler, ok := s.actionHandlers[actionType]
	if !ok {
		return fmt.Errorf("handler not found for agent action=%s", actionType)
	}

	if err := handler.Handle(ctx, data); err != nil {
		return err
	}
	return nil
}

func (s *service) ackAction(ctx context.Context, action *castai.ClusterAction, handleErr error) error {
	actionType := reflect.TypeOf(action.Data())
	s.log.Infof("ack action, id=%s, type=%s", action.ID, actionType)

	return backoff.RetryNotify(func() error {
		ctx, cancel := context.WithTimeout(ctx, s.cfg.AckTimeout)
		defer cancel()
		return s.castaiClient.AckAction(ctx, action.ID, &castai.AckClusterActionRequest{
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
