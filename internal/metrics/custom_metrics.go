package metrics

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// actionStartedCounter tracks actions started by the cluster controller.
var actionStartedCounter = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "action_started_total",
		Help: "Count of actions started by type.",
	},
	[]string{"type"},
)

// actionExecutedCounter tracks actions executed by the cluster controller.
var actionExecutedCounter = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "action_executed_total",
		Help: "Count of successful and unsuccessful actions executed by type.",
	},
	[]string{"success", "type"},
)

// actionExecutedDuration tracks the duration of actions executed by the cluster controller.
// Summary is used instead of histogram to use less series.
// We cannot run aggregations on Prometheus side with Summaries,
// but there is only a single active cluster controller pod,
// so this might not be a problem.
var actionExecutedDuration = prometheus.NewSummaryVec(
	prometheus.SummaryOpts{
		Name: "action_executed_duration_seconds",
		Help: "Duration of actions executed by type.",
		Objectives: map[float64]float64{
			0.5:  0.05,
			0.9:  0.01,
			0.99: 0.001,
		},
	},
	[]string{"type"},
)

func ActionStarted(actionType string) {
	actionStartedCounter.With(prometheus.Labels{"type": actionType}).Inc()
}

func ActionFinished(actionType string, success bool, duration time.Duration) {
	actionExecutedCounter.With(prometheus.Labels{"success": strconv.FormatBool(success), "type": actionType}).Inc()
	actionExecutedDuration.With(prometheus.Labels{"type": actionType}).Observe(duration.Seconds())
}

// informerCacheSize tracks the size of informer caches.
var informerCacheSize = prometheus.NewGaugeVec(
	prometheus.GaugeOpts{
		Name: "informer_cache_size",
		Help: "Number of objects in informer cache by resource type.",
	},
	[]string{"resource"},
)

// informerCacheSyncs tracks informer cache sync attempts.
var informerCacheSyncs = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "informer_cache_syncs_total",
		Help: "Informer cache sync attempts by resource and status.",
	},
	[]string{"resource", "status"},
)

func SetInformerCacheSize(resource string, size int) {
	informerCacheSize.With(prometheus.Labels{"resource": resource}).Set(float64(size))
}

func IncrementInformerCacheSyncs(resource, status string) {
	informerCacheSyncs.With(prometheus.Labels{"resource": resource, "status": status}).Inc()
}
