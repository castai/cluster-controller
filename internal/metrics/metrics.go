package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"k8s.io/component-base/metrics/legacyregistry"
)

var (
	// registry = metrics.NewKubeRegistry()
	registry = prometheus.NewRegistry()
)

func NewMetricsMux() *http.ServeMux {
	// Implementation inspired from https://github.com/kubernetes/kubernetes/pull/118081 and metrics-server.
	// Client-go doesn't really have good docs on exporting metrics...
	metricsMux := http.NewServeMux()

	metricsMux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		// Handles clientgo and other metrics
		legacyregistry.Handler().ServeHTTP(w, r)
		// Handles other metrics like go runtime, our custom metrics, etc.
		promhttp.HandlerFor(registry, promhttp.HandlerOpts{}).ServeHTTP(w, r)
	})

	return metricsMux
}
