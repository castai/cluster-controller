package metrics

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"k8s.io/component-base/metrics/legacyregistry"
)

func NewMetricsMux() *http.ServeMux {
	metricsMux := http.NewServeMux()
	metricsMux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		// Handles clientgo and other metrics
		legacyregistry.Handler().ServeHTTP(w, r)
		// Handles other metrics like go runtime, our custom metrics, etc.
		promhttp.Handler().ServeHTTP(w, r)
	})

	return metricsMux
}
