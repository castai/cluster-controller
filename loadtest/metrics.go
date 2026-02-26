package loadtest

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// testRunCounter tracks test run iterations by status.
var testRunCounter = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "test_run_total",
		Help: "Count of test run iterations by status (success/failed).",
	},
	[]string{"status"},
)

// testRunErrorsCounter tracks total number of errors in failed test runs.
var testRunErrorsCounter = prometheus.NewCounter(
	prometheus.CounterOpts{
		Name: "test_run_errors_total",
		Help: "Total count of errors across all failed test runs.",
	},
)

// actionProcessingDuration tracks the duration from action creation to ack by action type and success.
var actionProcessingDuration = prometheus.NewHistogramVec(
	prometheus.HistogramOpts{
		Name:    "action_processing_duration_seconds",
		Help:    "Duration from action creation to acknowledgement by type and success.",
		Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60, 120, 300},
	},
	[]string{"type", "success"},
)

func IncrementTestRunSuccess() {
	testRunCounter.With(prometheus.Labels{"status": "success"}).Inc()
}

func IncrementTestRunFailure(errorCount int) {
	testRunCounter.With(prometheus.Labels{"status": "failed"}).Inc()
	testRunErrorsCounter.Add(float64(errorCount))
}

func RecordActionProcessingDuration(actionType string, success bool, duration time.Duration) {
	actionProcessingDuration.With(prometheus.Labels{
		"type":    actionType,
		"success": strconv.FormatBool(success),
	}).Observe(duration.Seconds())
}

func RegisterTestMetrics(registry prometheus.Registerer) {
	registry.MustRegister(
		testRunCounter,
		testRunErrorsCounter,
		actionProcessingDuration,
	)
}
