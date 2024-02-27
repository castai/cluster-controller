package castai

import (
	"context"
	"fmt"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/sirupsen/logrus"

	"github.com/castai/cluster-controller/config"
)

const (
	headerAPIKey            = "X-API-Key"
	headerUserAgent         = "User-Agent"
	headerKubernetesVersion = "X-K8s-Version"
)

type Client interface {
	GetActions(ctx context.Context, k8sVersion string) ([]*ClusterAction, error)
	AckAction(ctx context.Context, actionID string, req *AckClusterActionRequest) error
	SendLogs(ctx context.Context, req *LogEvent) error
	SendAKSInitData(ctx context.Context, req *AKSInitDataRequest) error
}

func NewClient(log *logrus.Logger, rest *resty.Client, clusterID string) Client {
	return &client{
		log:       log,
		rest:      rest,
		clusterID: clusterID,
	}
}

// NewDefaultClient configures a default instance of the resty.Client used to do HTTP requests.
func NewDefaultClient(url, key string, level logrus.Level, binVersion *config.ClusterControllerVersion) *resty.Client {
	client := resty.New()
	client.SetHostURL(url)
	client.SetTimeout(5 * time.Minute) // Hard timeout for any request.
	client.Header.Set(headerAPIKey, key)
	client.Header.Set(headerUserAgent, "castai-cluster-controller/"+binVersion.Version)
	if level == logrus.TraceLevel {
		client.SetDebug(true)
	}

	return client
}

type client struct {
	log       *logrus.Logger
	rest      *resty.Client
	clusterID string
}

func (c *client) SendAKSInitData(ctx context.Context, req *AKSInitDataRequest) error {
	resp, err := c.rest.R().
		SetBody(req).
		SetContext(ctx).
		Post(fmt.Sprintf("/v1/kubernetes/external-clusters/%s/aks-init-data", c.clusterID))

	if err != nil {
		return fmt.Errorf("sending aks init data: %w", err)
	}
	if resp.IsError() {
		return fmt.Errorf("sending aks init data: request error status_code=%d body=%s", resp.StatusCode(), resp.Body())
	}

	return nil
}

func (c *client) SendLogs(ctx context.Context, req *LogEvent) error {
	// Server expects fields values to be strings. If they're not it fails with BAD_REQUEST/400.
	// Alternatively we could use "google/protobuf/any.proto" on server side but ATM it doesn't work.
	for k, v := range req.Fields {
		switch v.(type) {
		case string:
		// do nothing
		default:
			req.Fields[k] = fmt.Sprint(v) // Force into string
		}
	}
	resp, err := c.rest.R().
		SetBody(req).
		SetContext(ctx).
		Post(fmt.Sprintf("/v1/kubernetes/clusters/%s/actions/logs", c.clusterID))

	if err != nil {
		return fmt.Errorf("sending logs: %w", err)
	}
	if resp.IsError() {
		return fmt.Errorf("sending logs: request error status_code=%d body=%s", resp.StatusCode(), resp.Body())
	}

	return nil
}

func (c *client) GetActions(ctx context.Context, k8sVersion string) ([]*ClusterAction, error) {
	res := &GetClusterActionsResponse{}
	resp, err := c.rest.R().
		SetContext(ctx).
		SetResult(res).
		SetHeader(headerKubernetesVersion, k8sVersion).
		Get(fmt.Sprintf("/v1/kubernetes/clusters/%s/actions", c.clusterID))
	if err != nil {
		return nil, fmt.Errorf("failed to request cluster-actions: %w", err)
	}
	if resp.IsError() {
		return nil, fmt.Errorf("get cluster-actions: request error host=%s, status_code=%d body=%s", c.rest.HostURL, resp.StatusCode(), resp.Body())
	}
	return res.Items, nil
}

func (c *client) AckAction(ctx context.Context, actionID string, req *AckClusterActionRequest) error {
	resp, err := c.rest.R().
		SetContext(ctx).
		SetBody(req).
		Post(fmt.Sprintf("/v1/kubernetes/clusters/%s/actions/%s/ack", c.clusterID, actionID))
	if err != nil {
		return fmt.Errorf("failed to request cluster-actions ack: %v", err)
	}
	if resp.IsError() {
		return fmt.Errorf("ack cluster-actions: request error status_code=%d body=%s", resp.StatusCode(), resp.Body())
	}
	return nil
}
