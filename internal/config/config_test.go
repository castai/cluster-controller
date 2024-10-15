package config

import (
	"os"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestConfig(t *testing.T) {
	require.NoError(t, os.Setenv("API_KEY", "abc"))
	require.NoError(t, os.Setenv("API_URL", "api.cast.ai"))
	require.NoError(t, os.Setenv("KUBECONFIG", "~/.kube/config"))
	require.NoError(t, os.Setenv("CLUSTER_ID", "c1"))
	require.NoError(t, os.Setenv("LEADER_ELECTION_ENABLED", "true"))
	require.NoError(t, os.Setenv("LEADER_ELECTION_NAMESPACE", "castai-agent"))
	require.NoError(t, os.Setenv("LEADER_ELECTION_LOCK_NAME", "castai-cluster-controller"))
	require.NoError(t, os.Setenv("LEADER_ELECTION_LEASE_DURATION", "25s"))
	require.NoError(t, os.Setenv("LEADER_ELECTION_LEASE_RENEW_DEADLINE", "20s"))

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
		ClusterID:  "c1",
		LeaderElection: LeaderElection{
			Enabled:            true,
			Namespace:          "castai-agent",
			LockName:           "castai-cluster-controller",
			LeaseDuration:      time.Second * 25,
			LeaseRenewDeadline: time.Second * 20,
		},
		KubeClient: KubeClient{
			QPS:   25,
			Burst: 150,
		},
	}

	require.Equal(t, expected, cfg)
}
