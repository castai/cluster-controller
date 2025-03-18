package metrics

import (
	"strconv"

	"github.com/prometheus/client_golang/prometheus"
)

// actionCounter tracks actions executed by the cluster controller.
var actionCounter = prometheus.NewCounterVec(
	prometheus.CounterOpts{
		Name: "action_executed_total",
		Help: "Count of successful and unsuccessful actions executed by type.",
	},
	[]string{"success", "type"},
)

func ActionFinished(actionType string, success bool) {
	actionCounter.With(prometheus.Labels{"success": strconv.FormatBool(success), "type": actionType}).Inc()
}
