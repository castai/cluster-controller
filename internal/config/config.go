package config

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"k8s.io/apimachinery/pkg/util/net"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/castai/cluster-controller/internal/waitext"
)

type Config struct {
	Log              Log
	API              API
	TLS              TLS
	Kubeconfig       string
	KubeClient       KubeClient
	ClusterID        string
	PprofPort        int
	Metrics          Metrics
	LeaderElection   LeaderElection
	Drain            Drain
	VolumeAttachment VolumeAttachment
	Informer         Informer
	SchedulingGates  SchedulingGates
	PodMonitor       PodMonitor
	// AutoscalingDisabled is a flag to disable approving csr.
	AutoscalingDisabled bool `mapstructure:"autoscaling_disabled"`
	// MaxActionsInProgress serves as a safeguard to limit the number of Goroutines in progress.
	MaxActionsInProgress int

	MonitorMetadataPath string `mapstructure:"monitor_metadata"`
	SelfPod             Pod    `mapstructure:"self_pod"`
}

type Pod struct {
	Namespace string `mapstructure:"namespace"`
	Name      string `mapstructure:"name"`
	Node      string `mapstructure:"node"`
}

type Log struct {
	Level uint32
}

type API struct {
	Key string
	URL string
}

type TLS struct {
	CACert string
}

type Metrics struct {
	Port int
	// ExportEnabled enabled exporting metrics to Cast AI SaaS platform.
	ExportEnabled bool
	// ExportInterval is the interval at which metrics are exported to Cast AI SaaS platform.
	ExportInterval time.Duration
}

type LeaderElection struct {
	Enabled            bool
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

type Drain struct {
	// DisableVolumeDetachWait disables waiting for volume detach during node drain, ignoring
	// per action settings.
	DisableVolumeDetachWait bool `mapstructure:"disablevolumedetachwait"`
}

type VolumeAttachment struct {
	// DefaultTimeout is the default timeout for waiting for VolumeAttachments to detach.
	// Used if not specified in the drain action.
	DefaultTimeout time.Duration `mapstructure:"defaulttimeout"`
}

type Informer struct {
	EnablePod        bool          `mapstructure:"enablepod"`
	EnableNode       bool          `mapstructure:"enablenode"`
	CacheSyncTimeout time.Duration `mapstructure:"cachesynctimeout"`
}

type SchedulingGates struct {
	Enabled bool `mapstructure:"enabled"`
}

type PodMonitor struct {
	Enabled  bool          `mapstructure:"enabled"`
	Interval time.Duration `mapstructure:"interval"`
	Duration time.Duration `mapstructure:"duration"`
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
	_ = viper.BindEnv("tls.cacert", "TLS_CA_CERT_FILE")
	_ = viper.BindEnv("clusterid", "CLUSTER_ID")
	_ = viper.BindEnv("kubeconfig")
	_ = viper.BindEnv("kubeclient.qps", "KUBECLIENT_QPS")
	_ = viper.BindEnv("kubeclient.burst", "KUBECLIENT_BURST")
	_ = viper.BindEnv("pprofport", "PPROF_PORT")
	_ = viper.BindEnv("leaderelection.enabled", "LEADER_ELECTION_ENABLED")
	_ = viper.BindEnv("leaderelection.lockname", "LEADER_ELECTION_LOCK_NAME")
	_ = viper.BindEnv("leaderelection.leaseduration", "LEADER_ELECTION_LEASE_DURATION")
	_ = viper.BindEnv("leaderelection.leaserenewdeadline", "LEADER_ELECTION_LEASE_RENEW_DEADLINE")
	_ = viper.BindEnv("monitor_metadata", "MONITOR_METADATA")
	_ = viper.BindEnv("self_pod.node", "KUBERNETES_NODE_NAME")
	_ = viper.BindEnv("self_pod.name", "KUBERNETES_POD")
	_ = viper.BindEnv("self_pod.namespace", "LEADER_ELECTION_NAMESPACE")
	_ = viper.BindEnv("max_action_in_progress", "MAX_ACTIONS_IN_PROGRESS")
	_ = viper.BindEnv("autoscaling_disabled", "AUTOSCALING_DISABLED")
	_ = viper.BindEnv("metrics.port", "METRICS_PORT")
	_ = viper.BindEnv("metrics.exportenabled", "METRICS_EXPORT_ENABLED")
	_ = viper.BindEnv("metrics.exportinterval", "METRICS_EXPORT_INTERVAL")
	_ = viper.BindEnv("drain.disablevolumedetachwait", "DRAIN_DISABLE_VOLUME_DETACH_WAIT")
	_ = viper.BindEnv("volumeattachment.defaulttimeout", "VOLUME_ATTACHMENT_DEFAULT_TIMEOUT")
	_ = viper.BindEnv("informer.enablepod", "INFORMER_ENABLE_POD")
	_ = viper.BindEnv("informer.enablenode", "INFORMER_ENABLE_NODE")
	_ = viper.BindEnv("informer.cachesynctimeout", "INFORMER_CACHE_SYNC_TIMEOUT")
	_ = viper.BindEnv("schedulinggates.enabled", "SCHEDULING_GATES_ENABLED")
	_ = viper.BindEnv("podmonitor.enabled", "POD_MONITOR_ENABLED")
	_ = viper.BindEnv("podmonitor.interval", "POD_MONITOR_INTERVAL")
	_ = viper.BindEnv("podmonitor.duration", "POD_MONITOR_DURATION")

	cfg = &Config{}
	if err := viper.Unmarshal(&cfg); err != nil {
		panic(fmt.Errorf("parsing configuration: %w", err))
	}

	if cfg.Log.Level == 0 {
		cfg.Log.Level = uint32(logrus.InfoLevel)
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

	if _, err := uuid.Parse(cfg.ClusterID); err != nil {
		panic(fmt.Errorf("parsing cluster id: %w", err))
	}

	if cfg.SelfPod.Namespace == "" {
		// LEADER_ELECTION_NAMESPACE exists for backwards compatibility.
		// But we use the namespace even without leader election so it's required.
		required("self_pod.namespace or LEADER_ELECTION_NAMESPACE")
	}

	if cfg.LeaderElection.Enabled {
		if cfg.LeaderElection.LockName == "" {
			required("LEADER_ELECTION_LOCK_NAME")
		}
		if cfg.LeaderElection.LeaseDuration == 0 {
			cfg.LeaderElection.LeaseDuration = 15 * time.Second
		}
		if cfg.LeaderElection.LeaseRenewDeadline == 0 {
			cfg.LeaderElection.LeaseRenewDeadline = 10 * time.Second
		}
	}
	if cfg.KubeClient.QPS == 0 {
		cfg.KubeClient.QPS = 25
	}
	if cfg.KubeClient.Burst == 0 {
		cfg.KubeClient.Burst = 150
	}

	if cfg.MaxActionsInProgress == 0 {
		cfg.MaxActionsInProgress = 1000
	}

	if cfg.Metrics.Port == 0 {
		cfg.Metrics.Port = 9090
	}

	if cfg.Metrics.ExportInterval < 15*time.Second {
		// We do not want to export metrics too often
		// and also protect against accidental misconfiguration.
		cfg.Metrics.ExportInterval = 30 * time.Second
	}

	if cfg.VolumeAttachment.DefaultTimeout == 0 {
		cfg.VolumeAttachment.DefaultTimeout = 60 * time.Second
	}

	if cfg.Informer.CacheSyncTimeout == 0 {
		cfg.Informer.CacheSyncTimeout = 1 * time.Minute
	}

	if cfg.PodMonitor.Interval == 0 {
		cfg.PodMonitor.Interval = 10 * time.Second
	}

	if cfg.PodMonitor.Duration == 0 {
		cfg.PodMonitor.Duration = 5 * time.Minute
	}

	return *cfg
}

func required(variable string) {
	panic(fmt.Errorf("env variable %s is required", variable))
}

func kubeConfigFromEnv() (*rest.Config, error) {
	kubepath := Get().Kubeconfig
	if kubepath == "" {
		return nil, nil
	}

	data, err := os.ReadFile(kubepath)
	if err != nil {
		return nil, fmt.Errorf("reading kubeconfig at %s: %w", kubepath, err)
	}

	restConfig, err := clientcmd.RESTConfigFromKubeConfig(data)
	if err != nil {
		return nil, fmt.Errorf("building rest config from kubeconfig at %s: %w", kubepath, err)
	}

	return restConfig, nil
}

func RetrieveKubeConfig(log logrus.FieldLogger) (*rest.Config, error) {
	kubeconfig, err := kubeConfigFromEnv()
	if err != nil {
		return nil, err
	}

	if kubeconfig != nil {
		log.Debug("using kubeconfig from env variables")
		return kubeconfig, nil
	}

	inClusterConfig, err := rest.InClusterConfig()
	if err != nil {
		return nil, err
	}
	inClusterConfig.Wrap(func(rt http.RoundTripper) http.RoundTripper {
		return &kubeRetryTransport{
			log:           log,
			next:          rt,
			maxRetries:    10,
			retryInterval: 3 * time.Second,
		}
	})
	log.Debug("using in cluster kubeconfig")

	return inClusterConfig, nil
}

type kubeRetryTransport struct {
	log           logrus.FieldLogger
	next          http.RoundTripper
	maxRetries    int
	retryInterval time.Duration
}

func (rt *kubeRetryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var resp *http.Response

	boff := waitext.NewConstantBackoff(rt.retryInterval)

	err := waitext.Retry(context.Background(), boff, rt.maxRetries, func(_ context.Context) (bool, error) {
		var err error
		resp, err = rt.next.RoundTrip(req)
		if err != nil {
			// Previously client-go contained logic to retry connection refused errors. See https://github.com/kubernetes/kubernetes/pull/88267/files
			if net.IsConnectionRefused(err) {
				return true, err
			}
			return false, err
		}
		return false, nil
	}, func(err error) {
		rt.log.Warnf("kube api server connection refused, will retry: %v", err)
	})
	return resp, err
}
