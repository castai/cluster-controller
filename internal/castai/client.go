//go:generate mockgen -destination ./mock/client.go . CastAIClient
package castai

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"math"
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
	podName   string
}

// NewClient returns new Client for communicating with Cast AI.
func NewClient(log *logrus.Logger, rest *resty.Client, clusterID, podName string) *Client {
	return &Client{
		log:       log,
		rest:      rest,
		clusterID: clusterID,
		podName:   podName,
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
	req := convertPrometheusMetricFamilies(gatherTime, c.podName, metricFamilies)

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

func convertPrometheusMetricFamilies(gatherTime time.Time, podName string, metricFamilies []*dto.MetricFamily) *PrometheusWriteRequest {
	timestamp := gatherTime.UnixMilli()

	timeseries := []PrometheusTimeseries{}
	for _, family := range metricFamilies {
		for _, metric := range family.Metric {
			commonLabels := []PrometheusLabel{
				{
					Name:  "pod_name",
					Value: podName,
				},
			}

			for _, label := range metric.Label {
				if label.Name == nil {
					continue
				}

				commonLabels = append(commonLabels, PrometheusLabel{
					Name:  *label.Name,
					Value: lo.FromPtr(label.Value),
				})
			}

			if metric.Counter != nil {
				timeseries = append(timeseries,
					convertPrometheusCounterMetric(commonLabels, family, metric, timestamp)...,
				)
			}

			if metric.Histogram != nil {
				timeseries = append(timeseries,
					convertPrometheusHistogramMetric(commonLabels, family, metric, timestamp)...)
			}

			if metric.Summary != nil {
				timeseries = append(timeseries,
					convertPrometheusSummaryMetric(commonLabels, family, metric, timestamp)...)
			}
		}
	}

	return &PrometheusWriteRequest{
		Timeseries: timeseries,
	}
}

func convertPrometheusCounterMetric(
	commonLabels []PrometheusLabel,
	family *dto.MetricFamily,
	metric *dto.Metric,
	timestamp int64,
) []PrometheusTimeseries {
	return []PrometheusTimeseries{
		{
			Labels: copyLabelsWithName(commonLabels, family.GetName()),
			Samples: []PrometheusSample{
				{
					Timestamp: timestamp,
					Value:     metric.Counter.GetValue(),
				},
			},
		},
	}
}

func convertPrometheusHistogramMetric(
	commonLabels []PrometheusLabel,
	family *dto.MetricFamily,
	metric *dto.Metric,
	timestamp int64,
) []PrometheusTimeseries {
	timeseries := make([]PrometheusTimeseries, 0)
	h := metric.Histogram

	for _, b := range h.Bucket {
		timeseries = append(timeseries, PrometheusTimeseries{
			Labels: copyLabelsWithName(commonLabels, family.GetName()+"_bucket", PrometheusLabel{
				Name: "le", Value: fmt.Sprintf("%f", b.GetUpperBound()),
			}),
			Samples: []PrometheusSample{
				{
					Timestamp: timestamp,
					Value:     float64(b.GetCumulativeCount()),
				},
			},
		})
	}
	// We need this +Inf bucket for histogram_quantile query.
	timeseries = append(timeseries, PrometheusTimeseries{
		Labels: copyLabelsWithName(commonLabels, family.GetName()+"_bucket", PrometheusLabel{
			Name: "le", Value: "+Inf",
		}),
		Samples: []PrometheusSample{
			{
				Timestamp: timestamp,
				Value:     float64(h.GetSampleCount()),
			},
		},
	})

	timeseries = append(timeseries, PrometheusTimeseries{
		Labels: copyLabelsWithName(commonLabels, family.GetName()+"_sum"),
		Samples: []PrometheusSample{
			{
				Timestamp: timestamp,
				Value:     h.GetSampleSum(),
			},
		},
	})

	timeseries = append(timeseries, PrometheusTimeseries{
		Labels: copyLabelsWithName(commonLabels, family.GetName()+"_count"),
		Samples: []PrometheusSample{
			{
				Timestamp: timestamp,
				Value:     float64(h.GetSampleCount()),
			},
		},
	})

	return timeseries
}

func convertPrometheusSummaryMetric(
	commonLabels []PrometheusLabel,
	family *dto.MetricFamily,
	metric *dto.Metric,
	timestamp int64,
) []PrometheusTimeseries {
	timeseries := make([]PrometheusTimeseries, 0)
	s := metric.Summary

	for _, quantile := range s.Quantile {
		if math.IsNaN(quantile.GetValue()) {
			continue
		}

		timeseries = append(timeseries, PrometheusTimeseries{
			Labels: copyLabelsWithName(commonLabels, family.GetName()+"_quantile", PrometheusLabel{
				Name:  "quantile",
				Value: fmt.Sprintf("%f", quantile.GetQuantile()),
			}),
			Samples: []PrometheusSample{
				{
					Timestamp: timestamp,
					Value:     quantile.GetValue(),
				},
			},
		})
	}

	timeseries = append(timeseries, PrometheusTimeseries{
		Labels: copyLabelsWithName(commonLabels, family.GetName()+"_sum"),
		Samples: []PrometheusSample{
			{
				Timestamp: timestamp,
				Value:     s.GetSampleSum(),
			},
		},
	})

	timeseries = append(timeseries, PrometheusTimeseries{
		Labels: copyLabelsWithName(commonLabels, family.GetName()+"_count"),
		Samples: []PrometheusSample{
			{
				Timestamp: timestamp,
				Value:     float64(s.GetSampleCount()),
			},
		},
	})
	return timeseries
}

func copyLabelsWithName(baseLabels []PrometheusLabel, name string, additionalLabels ...PrometheusLabel) []PrometheusLabel {
	labels := make([]PrometheusLabel, 0, len(baseLabels)+1)
	labels = append(labels, baseLabels...)
	labels = append(labels, additionalLabels...)
	labels = append(labels, PrometheusLabel{
		Name:  "__name__",
		Value: name,
	})
	return labels
}
