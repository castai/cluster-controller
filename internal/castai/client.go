//go:generate mockgen -destination ./mock/client.go . CastAIClient
package castai

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/go-resty/resty/v2"
	dto "github.com/prometheus/client_model/go"
	"github.com/samber/lo"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/http2"

	"github.com/castai/cluster-controller/internal/config"
)

const (
	headerAPIKey            = "X-API-Key" //nolint: gosec
	headerUserAgent         = "User-Agent"
	headerKubernetesVersion = "X-K8s-Version"
)

// CastAIClient lists functions used by actions package.
type CastAIClient interface {
	GetActions(ctx context.Context, k8sVersion string) ([]*ClusterAction, error)
	AckAction(ctx context.Context, actionID string, req *AckClusterActionRequest) error
	SendLog(ctx context.Context, e *LogEntry) error
	SendMetrics(ctx context.Context, gatherTime time.Time, metricFamilies []*dto.MetricFamily) error
}

type LogEntry struct {
	Level   string        `json:"level"`
	Time    time.Time     `json:"time"`
	Message string        `json:"message"`
	Fields  logrus.Fields `json:"fields"`
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

// NewRestyClient configures a default instance of the resty.Client used to do HTTP requests.
func NewRestyClient(url, key, ca string, level logrus.Level, binVersion *config.ClusterControllerVersion, defaultTimeout time.Duration) (*resty.Client, error) {
	clientTransport, err := createHTTPTransport(ca)
	if err != nil {
		return nil, err
	}

	client := resty.NewWithClient(&http.Client{
		Timeout:   defaultTimeout,
		Transport: clientTransport,
	})

	client.SetBaseURL(url)
	client.Header.Set(headerAPIKey, key)
	client.Header.Set(headerUserAgent, "castai-cluster-controller/"+binVersion.Version)
	if level == logrus.TraceLevel {
		client.SetDebug(true)
	}

	return client, nil
}

func createHTTPTransport(ca string) (*http.Transport, error) {
	tlsConfig, err := createTLSConfig(ca)
	if err != nil {
		return nil, fmt.Errorf("creating TLS config: %w", err)
	}
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
		TLSClientConfig:       tlsConfig,
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

func createTLSConfig(ca string) (*tls.Config, error) {
	if len(ca) == 0 {
		return nil, nil
	}

	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM([]byte(ca)) {
		return nil, fmt.Errorf("failed to add root certificate to CA pool")
	}

	return &tls.Config{
		RootCAs:    certPool,
		MinVersion: tls.VersionTLS12,
	}, nil
}

func (c *Client) SendLog(ctx context.Context, e *LogEntry) error {
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

func (c *Client) SendMetrics(ctx context.Context, gatherTime time.Time, metricFamilies []*dto.MetricFamily) error {
	req := convertPrometheusMetricFamilies(gatherTime, metricFamilies)

	resp, err := c.rest.R().
		SetBody(req).
		SetContext(ctx).
		Post(fmt.Sprintf("/v1/clusters/%s/components/%s/metrics", c.clusterID, "cluster-controller"))
	if err != nil {
		return fmt.Errorf("sending metrics: %w", err)
	}
	if resp.IsError() {
		return fmt.Errorf("sending metrics: request error status_code=%d body=%s", resp.StatusCode(), resp.Body())
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
		return nil, fmt.Errorf("get cluster-actions: request error host=%s, status_code=%d body=%s", c.rest.BaseURL, resp.StatusCode(), resp.Body())
	}
	return res.Items, nil
}

func (c *Client) AckAction(ctx context.Context, actionID string, req *AckClusterActionRequest) error {
	resp, err := c.rest.R().
		SetContext(ctx).
		SetBody(req).
		Post(fmt.Sprintf("/v1/kubernetes/clusters/%s/actions/%s/ack", c.clusterID, actionID))
	if err != nil {
		return fmt.Errorf("failed to request cluster-actions ack: %w", err)
	}
	if resp.IsError() {
		return fmt.Errorf("ack cluster-actions: request error status_code=%d body=%s", resp.StatusCode(), resp.Body())
	}
	return nil
}

func convertPrometheusMetricFamilies(gatherTime time.Time, metricFamilies []*dto.MetricFamily) *PrometheusWriteRequest {
	timestamp := gatherTime.UnixMilli()

	timeseries := []PrometheusTimeseries{}
	for _, family := range metricFamilies {
		for _, metric := range family.Metric {
			timeserie := PrometheusTimeseries{
				Labels: []PrometheusLabel{
					{
						Name:  "__name__",
						Value: family.GetName(),
					},
				},
			}
			for _, label := range metric.Label {
				if label.Name == nil {
					continue
				}

				timeserie.Labels = append(timeserie.Labels, PrometheusLabel{
					Name:  *label.Name,
					Value: lo.FromPtr(label.Value),
				})
			}

			if metric.Counter != nil {
				timeserie.Samples = []PrometheusSample{}
				timeserie.Samples = append(timeserie.Samples, PrometheusSample{
					Timestamp: timestamp,
					Value:     metric.Counter.GetValue(),
				})
			}

			timeseries = append(timeseries, timeserie)
		}
	}

	return &PrometheusWriteRequest{
		Timeseries: timeseries,
	}
}
