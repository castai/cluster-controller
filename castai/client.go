package castai

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/go-resty/resty/v2"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/http2"

	"github.com/castai/cluster-controller/config"
)

const (
	headerAPIKey            = "X-API-Key"
	headerUserAgent         = "User-Agent"
	headerKubernetesVersion = "X-K8s-Version"
)

// ActionsClient lists functions used by actions package.
// TODO: move interface into actions package.
type ActionsClient interface {
	GetActions(ctx context.Context, k8sVersion string) ([]*ClusterAction, error)
	AckAction(ctx context.Context, actionID string, req *AckClusterActionRequest) error
	SendAKSInitData(ctx context.Context, req *AKSInitDataRequest) error
}

// Client talks to Cast AI. It can poll and acknowledge actions
// and also inject logs.
type Client struct {
	log       *logrus.Logger
	rest      *resty.Client
	clusterID string
}

// NewClient returns new Client for communicating with Cast AI.
func NewClient(log *logrus.Logger, rest *resty.Client, clusterID string) *Client {
	return &Client{
		log:       log,
		rest:      rest,
		clusterID: clusterID,
	}
}

// NewDefaultClient configures a default instance of the resty.Client used to do HTTP requests.
func NewDefaultClient(url, key string, level logrus.Level, binVersion *config.ClusterControllerVersion, defaultTimeout time.Duration) (*resty.Client, error) {
	clientTransport, err := createHTTPTransport()
	if err != nil {
		return nil, err
	}

	client := resty.NewWithClient(&http.Client{
		Timeout:   defaultTimeout,
		Transport: clientTransport,
	})

	client.SetHostURL(url)
	client.Header.Set(headerAPIKey, key)
	client.Header.Set(headerUserAgent, "castai-cluster-controller/"+binVersion.Version)
	if level == logrus.TraceLevel {
		client.SetDebug(true)
	}

	return client, nil
}

func createHTTPTransport() (*http.Transport, error) {
	t1 := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout: 15 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	t2, err := http2.ConfigureTransports(t1)
	if err != nil {
		return nil, fmt.Errorf("failed to configure HTTP2 transport: %w", err)
	}
	// Adding timeout settings to the http2 transport to prevent bad tcp connection hanging the requests for too long
	// Doc: https://pkg.go.dev/golang.org/x/net/http2#Transport
	//  - ReadIdleTimeout is the time before a ping is sent when no frame has been received from a connection
	//  - PingTimeout is the time before the TCP connection being closed if a Ping response is not received
	// So in total, if a TCP connection goes bad, it would take the combined time before the TCP connection is closed
	t2.ReadIdleTimeout = 30 * time.Second
	t2.PingTimeout = 15 * time.Second

	return t1, nil
}

func (c *Client) SendAKSInitData(ctx context.Context, req *AKSInitDataRequest) error {
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

func (c *Client) SendLog(ctx context.Context, e *logEntry) error {
	// Server expects fields values to be strings. If they're not it fails with BAD_REQUEST/400.
	// Alternatively we could use "google/protobuf/any.proto" on server side but ATM it doesn't work.
	for k, v := range e.Fields {
		switch v.(type) {
		case string:
		// do nothing
		default:
			e.Fields[k] = fmt.Sprint(v) // Force into string
		}
	}
	resp, err := c.rest.R().
		SetBody(e).
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

func (c *Client) GetActions(ctx context.Context, k8sVersion string) ([]*ClusterAction, error) {
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

func (c *Client) AckAction(ctx context.Context, actionID string, req *AckClusterActionRequest) error {
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
