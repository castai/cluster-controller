package loadtest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/castai/cluster-controller/internal/castai"
)

func NewHttpServer(ctx context.Context, cfg Config, testServer *CastAITestServer) error {
	http.HandleFunc("/v1/kubernetes/clusters/{cluster_id}/actions", func(w http.ResponseWriter, r *http.Request) {
		result, err := testServer.GetActions(r.Context(), "")
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		response := &castai.GetClusterActionsResponse{
			Items: result,
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(response); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	})

	http.HandleFunc("/v1/kubernetes/clusters/{cluster_id}/actions/{action_id}/ack", func(w http.ResponseWriter, r *http.Request) {
		actionID := r.PathValue("action_id")
		var req castai.AckClusterActionRequest
		err := json.NewDecoder(r.Body).Decode(&req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		err = testServer.AckAction(r.Context(), actionID, &req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	})

	http.HandleFunc("/v1/kubernetes/clusters/{cluster_id}/actions/logs", func(w http.ResponseWriter, r *http.Request) {
		var req castai.LogEntry
		err := json.NewDecoder(r.Body).Decode(&req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		err = testServer.SendLog(r.Context(), &req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	})

	//nolint:gosec // Missing timeouts are not a real issue here.
	return http.ListenAndServe(fmt.Sprintf(":%d", cfg.Port), nil)
}
