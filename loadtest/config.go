package loadtest

import (
	"fmt"
	"time"

	"github.com/spf13/viper"
)

// Config for the HTTP server.
type Config struct {
	// Port where the mock server to listen on.
	Port int

	// KubeConfig can point to a kubeconfig file. If empty, InCluster client will be assumed.
	KubeConfig string
}

// TestServerConfig has settings for the mock server instance.
type TestServerConfig struct {
	// MaxActionsPerCall is the upper limit of actions to return in one CastAITestServer.GetActions call.
	MaxActionsPerCall int
	// TimeoutWaitingForActions controls how long to wait for at least 1 action to appear on server side.
	// This mimics CH behavior of not returning early if there are no pending actions and keeping the request "running".
	TimeoutWaitingForActions time.Duration
	// BufferSize controls the input channel size.
	BufferSize int
}

var singletonCfg *Config

func GetConfig() Config {
	// not thread safe, but you will not put this under concurrent pressure, right?
	if singletonCfg != nil {
		return *singletonCfg
	}

	_ = viper.BindEnv("port", "PORT")
	_ = viper.BindEnv("kubeconfig", "KUBECONFIG")

	singletonCfg = &Config{}
	if err := viper.Unmarshal(&singletonCfg); err != nil {
		panic(fmt.Errorf("parsing configuration: %w", err))
	}

	if singletonCfg.Port == 0 {
		panic(fmt.Errorf("test server port must be set"))
	}

	return *singletonCfg
}
