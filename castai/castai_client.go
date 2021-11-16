package castai

import (
	"context"
	"fmt"
	"net/http"

	"github.com/go-resty/resty/v2"
	"github.com/sirupsen/logrus"
)

const (
	headerAPIKey = "X-API-Key"
)

var (
	hdrAPIKey = http.CanonicalHeaderKey(headerAPIKey)
)

type Client interface {
	GetActions(ctx context.Context, clusterID string) ([]*ClusterAction, error)
	AckAction(ctx context.Context, clusterID, actionID string, req *AckClusterActionRequest) error
}

func NewClient(log *logrus.Logger, rest *resty.Client) Client {
	return &client{
		log:  log,
		rest: rest,
	}
}

// NewDefaultClient configures a default instance of the resty.Client used to do HTTP requests.
func NewDefaultClient(url, key string, level logrus.Level) *resty.Client {
	client := resty.New()
	client.SetHostURL(url)
	client.Header.Set(hdrAPIKey, key)
	if level == logrus.TraceLevel {
		client.SetDebug(true)
	}

	return client
}

type client struct {
	log  *logrus.Logger
	rest *resty.Client
}

func (c *client) GetActions(ctx context.Context, clusterID string) ([]*ClusterAction, error) {
	res := &GetClusterActionsResponse{}
	resp, err := c.rest.R().
		SetContext(ctx).
		SetResult(res).
		Get(fmt.Sprintf("/v1/kubernetes/clusters/%s/actions", clusterID))
	if err != nil {
		return nil, fmt.Errorf("failed to request cluster-actions: %w", err)
	}
	if resp.IsError() {
		return nil, fmt.Errorf("get cluster-actions: request error host=%s, status_code=%d body=%s", c.rest.HostURL, resp.StatusCode(), resp.Body())
	}
	return res.Items, nil
}

func (c *client) AckAction(ctx context.Context, clusterID, actionID string, req *AckClusterActionRequest) error {
	resp, err := c.rest.R().
		SetContext(ctx).
		SetBody(req).
		Delete(fmt.Sprintf("/v1/kubernetes/clusters/%s/actions/%s/ack", clusterID, actionID))
	if err != nil {
		return fmt.Errorf("failed to request cluster-actions ack: %v", err)
	}
	if resp.IsError() {
		return fmt.Errorf("ack cluster-actions: request error status_code=%d body=%s", resp.StatusCode(), resp.Body())
	}
	return nil
}
