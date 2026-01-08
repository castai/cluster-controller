package config

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestConfig(t *testing.T) {
	clusterId := uuid.New().String()
	t.Setenv("API_KEY", "abc")
	t.Setenv("API_URL", "api.cast.ai")
	t.Setenv("KUBECONFIG", "~/.kube/config")
	t.Setenv("CLUSTER_ID", clusterId)
	t.Setenv("LEADER_ELECTION_ENABLED", "true")
	t.Setenv("LEADER_ELECTION_NAMESPACE", "castai-agent")
	t.Setenv("LEADER_ELECTION_LOCK_NAME", "castai-cluster-controller")
	t.Setenv("LEADER_ELECTION_LEASE_DURATION", "25s")
	t.Setenv("LEADER_ELECTION_LEASE_RENEW_DEADLINE", "20s")
	t.Setenv("METRICS_PORT", "16000")

	cfg := Get()

	expected := Config{
		Log: Log{
			Level: uint32(logrus.InfoLevel),
		},
		PprofPort: 6060,
		API: API{
			Key: "abc",
			URL: "api.cast.ai",
		},
		Kubeconfig: "~/.kube/config",
		SelfPod: Pod{
			Namespace: "castai-agent",
		},
		ClusterID: clusterId,
		LeaderElection: LeaderElection{
			Enabled:            true,
			LockName:           "castai-cluster-controller",
			LeaseDuration:      time.Second * 25,
			LeaseRenewDeadline: time.Second * 20,
		},
		KubeClient: KubeClient{
			QPS:   25,
			Burst: 150,
		},
		MaxActionsInProgress: 1000,
		Metrics: Metrics{
			Port:           16000,
			ExportEnabled:  false,
			ExportInterval: 30 * time.Second,
		},
		Drain: Drain{
			WaitForVolumeDetach: false,
			VolumeDetachTimeout: 60 * time.Second,
			CacheSyncTimeout:    120 * time.Second,
		},
	}

	require.Equal(t, expected, cfg)
}
