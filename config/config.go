package config

import (
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

type Config struct {
	Log            Log
	API            API
	Kubeconfig     string
	KubeClient     KubeClient
	ClusterID      string
	PprofPort      int
	LeaderElection LeaderElection
	PodName        string
	NodeName       string
}

type Log struct {
	Level int
}

type API struct {
	Key string
	URL string
}

type LeaderElection struct {
	Enabled            bool
	Namespace          string
	LockName           string
	LeaseDuration      time.Duration
	LeaseRenewDeadline time.Duration
}

type KubeClient struct {
	// K8S client rate limiter allows bursts of up to 'burst' to exceed the QPS, while still maintaining a
	// smoothed qps rate of 'qps'.
	// The bucket is initially filled with 'burst' tokens, and refills at a rate of 'qps'.
	// The maximum number of tokens in the bucket is capped at 'burst'.
	QPS   int
	Burst int
}

var cfg *Config

// Get configuration bound to environment variables.
func Get() Config {
	if cfg != nil {
		return *cfg
	}

	_ = viper.BindEnv("log.level", "LOG_LEVEL")
	_ = viper.BindEnv("api.key", "API_KEY")
	_ = viper.BindEnv("api.url", "API_URL")
	_ = viper.BindEnv("clusterid", "CLUSTER_ID")
	_ = viper.BindEnv("kubeconfig")
	_ = viper.BindEnv("kubeclient.qps", "KUBECLIENT_QPS")
	_ = viper.BindEnv("kubeclient.burst", "KUBECLIENT_BURST")
	_ = viper.BindEnv("pprofport", "PPROF_PORT")
	_ = viper.BindEnv("leaderelection.enabled", "LEADER_ELECTION_ENABLED")
	_ = viper.BindEnv("leaderelection.namespace", "LEADER_ELECTION_NAMESPACE")
	_ = viper.BindEnv("leaderelection.lockname", "LEADER_ELECTION_LOCK_NAME")
	_ = viper.BindEnv("leaderelection.leaseduration", "LEADER_ELECTION_LEASE_DURATION")
	_ = viper.BindEnv("leaderelection.leaserenewdeadline", "LEADER_ELECTION_LEASE_RENEW_DEADLINE")
	_ = viper.BindEnv("aksinitdata", "AKS_INIT_DATA")
	_ = viper.BindEnv("nodename", "KUBERNETES_NODE_NAME")
	_ = viper.BindEnv("podname", "KUBERNETES_POD")

	cfg = &Config{}
	if err := viper.Unmarshal(&cfg); err != nil {
		panic(fmt.Errorf("parsing configuration: %v", err))
	}

	if cfg.Log.Level == 0 {
		cfg.Log.Level = int(logrus.InfoLevel)
	}
	if cfg.PprofPort == 0 {
		cfg.PprofPort = 6060
	}
	if cfg.API.Key == "" {
		required("API_KEY")
	}
	if cfg.API.URL == "" {
		required("API_URL")
	}
	if cfg.ClusterID == "" {
		required("CLUSTER_ID")
	}
	if cfg.LeaderElection.Enabled {
		if cfg.LeaderElection.Namespace == "" {
			required("LEADER_ELECTION_NAMESPACE")
		}
		if cfg.LeaderElection.LockName == "" {
			required("LEADER_ELECTION_LOCK_NAME")
		}
		if cfg.LeaderElection.LeaseDuration == 0 {
			cfg.LeaderElection.LeaseDuration = 15 * time.Second
		} else {
			cfg.LeaderElection.LeaseDuration = cfg.LeaderElection.LeaseDuration
		}
		if cfg.LeaderElection.LeaseRenewDeadline == 0 {
			cfg.LeaderElection.LeaseRenewDeadline = 10 * time.Second
		} else {
			cfg.LeaderElection.LeaseRenewDeadline = cfg.LeaderElection.LeaseRenewDeadline
		}
	}
	if cfg.KubeClient.QPS == 0 {
		cfg.KubeClient.QPS = 25
	}
	if cfg.KubeClient.Burst == 0 {
		cfg.KubeClient.Burst = 150
	}

	return *cfg
}

func required(variable string) {
	panic(fmt.Errorf("env variable %s is required", variable))
}
