package config

import (
	"os"
	"testing"

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

	cfg := Get()

	require.Equal(t, "abc", cfg.API.Key)
	require.Equal(t, "api.cast.ai", cfg.API.URL)
	require.Equal(t, "~/.kube/config", cfg.Kubeconfig)
	require.Equal(t, "c1", cfg.ClusterID)
	require.Equal(t, true, cfg.LeaderElection.Enabled)
	require.Equal(t, "castai-agent", cfg.LeaderElection.Namespace)
	require.Equal(t, "castai-cluster-controller", cfg.LeaderElection.LockName)
}
