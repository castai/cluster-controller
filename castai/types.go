package castai

import (
	"time"

	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
)

type GetClusterActionsResponse struct {
	Items []*ClusterAction `json:"items"`
}

type AckClusterActionRequest struct {
	Error *string `json:"error"`
}

type ClusterAction struct {
	ID                string             `json:"id"`
	ActionDeleteNode  *ActionDeleteNode  `json:"actionDeleteNode,omitempty"`
	ActionDrainNode   *ActionDrainNode   `json:"actionDrainNode,omitempty"`
	ActionPatchNode   *ActionPatchNode   `json:"actionPatchNode,omitempty"`
	ActionCreateEvent *ActionCreateEvent `json:"actionCreateEvent,omitempty"`
	CreatedAt         time.Time          `json:"createdAt"`
	DoneAt            *time.Time         `json:"doneAt,omitempty"`
	Error             *string            `json:"error,omitempty"`
}

func (c *ClusterAction) Data() interface{} {
	if c.ActionDeleteNode != nil {
		return c.ActionDeleteNode
	}
	if c.ActionDrainNode != nil {
		return c.ActionDrainNode
	}
	if c.ActionPatchNode != nil {
		return c.ActionPatchNode
	}
	if c.ActionCreateEvent != nil {
		return c.ActionCreateEvent
	}
	return nil
}

type LogEvent struct {
	Level   string        `json:"level"`
	Time    time.Time     `json:"time"`
	Message string        `json:"message"`
	Fields  logrus.Fields `json:"fields"`
}

type ActionDeleteNode struct {
	NodeName string `json:"nodeName"`
}

type ActionDrainNode struct {
	NodeName            string `json:"nodeName"`
	DrainTimeoutSeconds int    `json:"drainTimeoutSeconds"`
	Force               bool   `json:"force"`
}

type ActionPatchNode struct {
	NodeName string            `json:"nodeName"`
	Labels   map[string]string `json:"labels"`
	Taints   []NodeTaint       `json:"taints"`
}

type NodeTaint struct {
	Effect string `json:"effect"`
	Key    string `json:"key"`
	Value  string `json:"value"`
}

type ActionCreateEvent struct {
	Reporter  string             `json:"reportingComponent"`
	ObjectRef v1.ObjectReference `json:"objectReference"`
	EventTime time.Time          `json:"eventTime"`
	EventType string             `json:"eventType"`
	Reason    string             `json:"reason"`
	Action    string             `json:"action"`
	Message   string             `json:"message"`
}
