package config

import (
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestConfig(t *testing.T) {
	clusterId := uuid.New().String()
	require.NoError(t, os.Setenv("API_KEY", "abc"))
	require.NoError(t, os.Setenv("API_URL", "api.cast.ai"))
	require.NoError(t, os.Setenv("KUBECONFIG", "~/.kube/config"))
	require.NoError(t, os.Setenv("CLUSTER_ID", clusterId))
	require.NoError(t, os.Setenv("LEADER_ELECTION_ENABLED", "true"))
	require.NoError(t, os.Setenv("LEADER_ELECTION_NAMESPACE", "castai-agent"))
	require.NoError(t, os.Setenv("LEADER_ELECTION_LOCK_NAME", "castai-cluster-controller"))
	require.NoError(t, os.Setenv("LEADER_ELECTION_LEASE_DURATION", "25s"))
	require.NoError(t, os.Setenv("LEADER_ELECTION_LEASE_RENEW_DEADLINE", "20s"))
	require.NoError(t, os.Setenv("METRICS_PORT", "16000"))

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
		Informer: InformerConfig{
			ResyncPeriod: 12 * time.Hour,
		},
	}

	require.Equal(t, expected, cfg)
}
