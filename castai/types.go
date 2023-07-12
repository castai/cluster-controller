package castai

import (
	"errors"
	"time"

	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
)

const (
	LabelProvisionerCastAINodeID = "provisioner.cast.ai/node-id:"
)

type GetClusterActionsResponse struct {
	Items []*ClusterAction `json:"items"`
}

type AckClusterActionRequest struct {
	Error *string `json:"error"`
}

type ClusterAction struct {
	ID                      string                   `json:"id"`
	ActionDeleteNode        *ActionDeleteNode        `json:"actionDeleteNode,omitempty"`
	ActionDrainNode         *ActionDrainNode         `json:"actionDrainNode,omitempty"`
	ActionPatchNode         *ActionPatchNode         `json:"actionPatchNode,omitempty"`
	ActionCreateEvent       *ActionCreateEvent       `json:"actionCreateEvent,omitempty"`
	ActionApproveCSR        *ActionApproveCSR        `json:"actionApproveCsr,omitempty"`
	ActionChartUpsert       *ActionChartUpsert       `json:"actionChartUpsert,omitempty"`
	ActionChartUninstall    *ActionChartUninstall    `json:"actionChartUninstall,omitempty"`
	ActionChartRollback     *ActionChartRollback     `json:"actionChartRollback,omitempty"`
	ActionDisconnectCluster *ActionDisconnectCluster `json:"actionDisconnectCluster,omitempty"`
	ActionSendAKSInitData   *ActionSendAKSInitData   `json:"actionSendAksInitData,omitempty"`
	ActionCheckNodeDeleted  *ActionCheckNodeDeleted  `json:"actionCheckNodeDeleted,omitempty"`
	ActionCheckNodeStatus   *ActionCheckNodeStatus   `json:"actionCheckNodeStatus,omitempty"`
	CreatedAt               time.Time                `json:"createdAt"`
	DoneAt                  *time.Time               `json:"doneAt,omitempty"`
	Error                   *string                  `json:"error,omitempty"`
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
	if c.ActionApproveCSR != nil {
		return c.ActionApproveCSR
	}
	if c.ActionChartUpsert != nil {
		return c.ActionChartUpsert
	}
	if c.ActionChartUninstall != nil {
		return c.ActionChartUninstall
	}
	if c.ActionChartRollback != nil {
		return c.ActionChartRollback
	}
	if c.ActionDisconnectCluster != nil {
		return c.ActionDisconnectCluster
	}
	if c.ActionSendAKSInitData != nil {
		return c.ActionSendAKSInitData
	}
	if c.ActionCheckNodeDeleted != nil {
		return c.ActionCheckNodeDeleted
	}
	if c.ActionCheckNodeStatus != nil {
		return c.ActionCheckNodeStatus
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
	NodeID   string `json:"nodeId"`
}

type ActionDrainNode struct {
	NodeName            string `json:"nodeName"`
	NodeID              string `json:"nodeId"`
	DrainTimeoutSeconds int    `json:"drainTimeoutSeconds"`
	Force               bool   `json:"force"`
}

type ActionApproveCSR struct {
	NodeName string `json:"nodeName"`
	NodeID   string `json:"nodeId"`
}

type ActionPatchNode struct {
	NodeName    string            `json:"nodeName"`
	NodeID      string            `json:"nodeId"`
	Labels      map[string]string `json:"labels"`
	Taints      []NodeTaint       `json:"taints"`
	Annotations map[string]string `json:"annotations"`
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

type ActionDisconnectCluster struct {
}

type ActionSendAKSInitData struct {
}

type ActionCheckNodeDeleted struct {
	NodeName string `json:"nodeName"`
	NodeID   string `json:"nodeId"`
}

type ActionCheckNodeStatus_Status string

const (
	ActionCheckNodeStatus_READY   ActionCheckNodeStatus_Status = "NodeStatus_READY"
	ActionCheckNodeStatus_DELETED ActionCheckNodeStatus_Status = "NodeStatus_DELETED"
)

type ActionCheckNodeStatus struct {
	NodeName           string                       `json:"nodeName"`
	NodeID             string                       `json:"nodeId"`
	NodeStatus         ActionCheckNodeStatus_Status `json:"nodeStatus,omitempty"`
	WaitTimeoutSeconds *int32                       `json:"waitTimeoutSeconds,omitempty"`
}

type ActionChartUpsert struct {
	Namespace       string            `json:"namespace"`
	ReleaseName     string            `json:"releaseName"`
	ValuesOverrides map[string]string `json:"valuesOverrides,omitempty"`
	ChartSource     ChartSource       `json:"chartSource"`
	CreateNamespace bool              `json:"createNamespace"`
}

type ActionChartUninstall struct {
	Namespace   string `json:"namespace"`
	ReleaseName string `json:"releaseName"`
}

type ActionChartRollback struct {
	Namespace   string `json:"namespace"`
	ReleaseName string `json:"releaseName"`
	Version     string `json:"version"`
}

type ChartSource struct {
	RepoURL string `json:"repoUrl"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

func (c *ChartSource) Validate() error {
	if c.Name == "" {
		return errors.New("chart name is not set")
	}
	if c.RepoURL == "" {
		return errors.New("chart repoURL is not set")
	}
	if c.Version == "" {
		return errors.New("chart version is not set")
	}
	return nil
}

type AKSInitDataRequest struct {
	CloudConfigBase64       string `json:"cloudConfigBase64"`
	ProtectedSettingsBase64 string `json:"protectedSettingsBase64"`
	Architecture            string `json:"architecture"`
}
