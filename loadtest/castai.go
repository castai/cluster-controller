package loadtest

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/samber/lo"

	"github.com/castai/cluster-controller/internal/castai"
)

// CastAITestServer acts as simple cluster hub mock replacement.
// It exposes a way to "push" actions to the cluster controller via GetActionsPushChannel
// and can be used as an implementation of the server interface that cluster controller expects to call.
type CastAITestServer struct {
	log                *slog.Logger
	actionsPushChannel chan castai.ClusterAction
	cfg                TestServerConfig
}

func NewTestServer(logger *slog.Logger, cfg TestServerConfig) *CastAITestServer {
	return &CastAITestServer{
		log:                logger,
		actionsPushChannel: make(chan castai.ClusterAction, cfg.BufferSize),
		cfg:                cfg,
	}
}

// GetActionsPushChannel returns a channel that can be used to push actions into the queue to be picked up by CC.
// Don't close the returned channel or pay the consequences!
func (c *CastAITestServer) GetActionsPushChannel() chan<- castai.ClusterAction {
	return c.actionsPushChannel
}

func (c *CastAITestServer) GetActions(ctx context.Context, _ string) ([]*castai.ClusterAction, error) {
	c.log.Info(fmt.Sprintf("GetActions called, have %d items in buffer", len(c.actionsPushChannel)))
	actionsToReturn := make([]*castai.ClusterAction, 0)

	// Wait for at least one action to arrive from whoever is pushing them.
	// If none arrive, we simulate the "empty poll" case of cluster-hub and return empty list.
	select {
	case x := <-c.actionsPushChannel:
		actionsToReturn = append(actionsToReturn, &x)
	case <-time.After(c.cfg.TimeoutWaitingForActions):
		c.log.Info(fmt.Sprintf("No actions to return in %v", c.cfg.TimeoutWaitingForActions))
		return nil, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("context done with cause (%v), err (%v)", context.Cause(ctx), ctx.Err())
	}

	// Attempt to drain up to max items from the channel.
	for len(actionsToReturn) <= c.cfg.MaxActionsPerCall {
		select {
		case x := <-c.actionsPushChannel:
			actionsToReturn = append(actionsToReturn, &x)
		case <-time.After(50 * time.Millisecond):
			// If we haven't received enough items, just flush.
			return actionsToReturn, nil
		case <-ctx.Done():
			return nil, fmt.Errorf("context done with cause (%v), err (%v)", context.Cause(ctx), ctx.Err())
		}
	}

	return actionsToReturn, nil
}

func (c *CastAITestServer) AckAction(ctx context.Context, actionID string, req *castai.AckClusterActionRequest) error {
	errMsg := lo.FromPtr(req.Error)
	c.log.DebugContext(ctx, fmt.Sprintf("action %q acknowledged; has error: %v; error: %v", actionID, req.Error != nil, errMsg))

	return nil
}

func (c *CastAITestServer) SendAKSInitData(ctx context.Context, req *castai.AKSInitDataRequest) error {
	return fmt.Errorf("not implemented; obsolete")
}

func (c *CastAITestServer) SendLog(ctx context.Context, e *castai.LogEntry) error {
	//var slogLvl slog.Level
	//switch e.Level {
	//case "INFO":
	//	slogLvl = slog.LevelInfo
	//case "DEBUG":
	//	slogLvl = slog.LevelDebug
	//case "WARN":
	//	slogLvl = slog.LevelWarn
	//case "ERROR":
	//	slogLvl = slog.LevelError
	//default:
	//	slogLvl = 100 // Some arbitrary value
	//}
	//
	//attrs := make([]slog.Attr, 0, len(e.Fields))
	//for k, v := range e.Fields {
	//	attrs = append(attrs, slog.Any(k, v))
	//}
	//
	//msg := fmt.Sprintf("log from controller: %s", e.Message)
	//
	//c.log.LogAttrs(ctx, slogLvl, msg, attrs...)

	return nil
}
