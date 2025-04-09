package loadtest

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
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

	logMx       sync.Mutex
	actionsLog  map[string]chan string
	lockActions sync.Mutex
	actions     map[string]*castai.ClusterAction
}

func NewTestServer(logger *slog.Logger, cfg TestServerConfig) *CastAITestServer {
	return &CastAITestServer{
		log:                logger,
		actionsPushChannel: make(chan castai.ClusterAction, 10000),
		cfg:                cfg,
		actionsLog:         make(map[string]chan string),
		actions:            make(map[string]*castai.ClusterAction),
	}
}

// ExecuteActions pushes the list of actions to the queue for cluster controller to process.
// This method returns when all actions are acked or context is cancelled.
func (c *CastAITestServer) ExecuteActions(ctx context.Context, actions []castai.ClusterAction) {
	// owner channel has 1:n relationship with the actions. It handles the ack
	ownerChannel := make(chan string, len(actions))

	for _, action := range actions {
		if action.ID == "" {
			action.ID = uuid.NewString()
		}
		if action.CreatedAt == (time.Time{}) {
			action.CreatedAt = time.Now()
		}
		c.addActionToStore(action.ID, action, ownerChannel)
	}
	c.log.Info(fmt.Sprintf("added %d actions to local DB", len(actions)))

	// Read from owner channel until len(actions) times, then close and return.
	finished := 0
	for {
		select {
		case <-ctx.Done():
			c.log.Info(fmt.Sprintf("Received signal to stop finished with cause (%q) and err (%v). Closing executor.", context.Cause(ctx), ctx.Err()))
			return
		case <-ownerChannel:
			finished++
			if finished == len(actions) {
				close(ownerChannel)
				return
			}
		}
	}
}

/* Start Cluster-hub mock implementation */

func (c *CastAITestServer) GetActions(ctx context.Context, _ string) ([]*castai.ClusterAction, error) {
	c.log.Info("GetActions called")
	c.logMx.Lock()
	actions := lo.MapToSlice(c.actions, func(_ string, value *castai.ClusterAction) *castai.ClusterAction {
		return value
	})
	c.logMx.Unlock()

	slices.SortStableFunc(actions, func(a, b *castai.ClusterAction) int {
		return a.CreatedAt.Compare(b.CreatedAt)
	})
	totalActionsInDB := len(actions)
	if totalActionsInDB > c.cfg.MaxActionsPerCall {
		actions = actions[:c.cfg.MaxActionsPerCall]
	}

	c.log.Info(fmt.Sprintf("Returning %d actions for processing out of %d", len(actions), totalActionsInDB))
	return actions, nil
}

func (c *CastAITestServer) AckAction(ctx context.Context, actionID string, req *castai.AckClusterActionRequest) error {
	errMsg := lo.FromPtr(req.Error)
	c.log.DebugContext(ctx, fmt.Sprintf("action %q acknowledged; has error: %v; error: %v", actionID, req.Error != nil, errMsg))

	receiver := c.removeActionFromStore(actionID)
	if receiver == nil {
		return fmt.Errorf("action %q does not have a receiver", actionID)
	}
	// Notify owner that this action was done.
	receiver <- actionID

	return nil
}

func (c *CastAITestServer) SendLog(ctx context.Context, e *castai.LogEntry) error {
	// No-op for now, maybe track metrics in the future?
	return nil
}

/* End Cluster-hub mock implementation */

func (c *CastAITestServer) addActionToStore(actionID string, action castai.ClusterAction, receiver chan string) {
	c.logMx.Lock()
	defer c.logMx.Unlock()

	c.actionsLog[actionID] = receiver
	c.actions[actionID] = &action
}

func (c *CastAITestServer) removeActionFromStore(actionID string) chan string {
	c.logMx.Lock()
	defer c.logMx.Unlock()

	receiver, ok := c.actionsLog[actionID]
	if !ok {
		c.log.Error(fmt.Sprintf("Receiver for action %s is no longer there, possibly shutting down or CC got restarted", actionID))
		receiver = nil
	}

	delete(c.actionsLog, actionID)
	delete(c.actions, actionID)

	return receiver
}
