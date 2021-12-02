//go:generate mockgen -destination ./mock/client.go . Client

package helm

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/strvals"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	memorycached "k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
)

type InstallOptions struct {
	Chart           *ChartCoordinates
	ValuesOverrides map[string]string
}

type UpgradeOptions struct {
	Chart           *ChartCoordinates
	Release         *release.Release
	ValuesOverrides map[string]string
}

type GetReleaseOptions struct {
	Namespace   string
	ReleaseName string
}

func NewClient(log logrus.FieldLogger, loader ChartLoader, restconfig *rest.Config) Client {
	return &client{
		log: log,
		configurationGetter: &configurationGetter{
			log:        log,
			helmDriver: "secrets",
			debug:      false,
		},
		chartLoader: loader,
		k8sConfig:   restconfig,
	}
}

type Client interface {
	Install(ctx context.Context, opts InstallOptions) (*release.Release, error)
	Upgrade(ctx context.Context, opts UpgradeOptions) (*release.Release, error)
	GetRelease(opts GetReleaseOptions) (*release.Release, error)
}

type client struct {
	log                 logrus.FieldLogger
	configurationGetter ConfigurationGetter
	chartLoader         ChartLoader
	k8sConfig           *rest.Config
}

func (c *client) Install(ctx context.Context, opts InstallOptions) (*release.Release, error) {
	ch, err := c.chartLoader.Load(ctx, opts.Chart)
	if err != nil {
		return nil, err
	}

	if req := ch.Metadata.Dependencies; req != nil {
		if err := action.CheckDependencies(ch, req); err != nil {
			return nil, err
		}
	}

	namespace := ch.Name()
	cfg, err := c.configurationGetter.Get(namespace, c.k8sConfig)
	if err != nil {
		return nil, err
	}

	install := action.NewInstall(cfg)
	install.ReleaseName = ch.Name()
	install.CreateNamespace = true
	install.Namespace = namespace
	install.Timeout = 10 * time.Minute
	install.Wait = true // Wait unit all applied resources are running.

	// Prepare user value overrides.
	values := map[string]interface{}{}
	if err := mergeValuesOverrides(values, opts.ValuesOverrides); err != nil {
		return nil, err
	}

	res, err := install.Run(ch, values)
	if err != nil {
		return nil, fmt.Errorf("running chart install, name=%s: %w", ch.Name(), err)
	}
	return res, err
}

func (c *client) Upgrade(ctx context.Context, opts UpgradeOptions) (*release.Release, error) {
	ch, err := c.chartLoader.Load(ctx, opts.Chart)
	if err != nil {
		return nil, err
	}

	if req := ch.Metadata.Dependencies; req != nil {
		if err := action.CheckDependencies(ch, req); err != nil {
			return nil, err
		}
	}

	namespace := opts.Release.Namespace

	cfg, err := c.configurationGetter.Get(namespace, c.k8sConfig)
	if err != nil {
		return nil, err
	}

	upgrade := action.NewUpgrade(cfg)
	upgrade.Namespace = namespace
	name := opts.Release.Name

	// Prepare user value overrides.
	values := map[string]interface{}{}
	if len(opts.Release.Config) > 0 {
		values = opts.Release.Config
	}
	if err := mergeValuesOverrides(values, opts.ValuesOverrides); err != nil {
		return nil, err
	}

	res, err := upgrade.Run(name, ch, values)
	if err != nil {
		return nil, fmt.Errorf("running chart upgrade, name=%s: %w", name, err)
	}
	return res, nil
}

func (c *client) GetRelease(opts GetReleaseOptions) (*release.Release, error) {
	cfg, err := c.configurationGetter.Get(opts.Namespace, c.k8sConfig)
	if err != nil {
		return nil, err
	}

	list := action.NewGet(cfg)
	rel, err := list.Run(opts.ReleaseName)
	if err != nil {
		return nil, fmt.Errorf("getting chart release, name=%s, namespace=%s: %w", opts.ReleaseName, opts.Namespace, err)
	}
	return rel, nil
}

// ConfigurationGetter wraps helm actions configuration setup for mocking in unit tests.
type ConfigurationGetter interface {
	Get(namespace string, restConfig *rest.Config) (*action.Configuration, error)
}

type configurationGetter struct {
	log        logrus.FieldLogger
	helmDriver string
	debug      bool
}

func (c *configurationGetter) Get(namespace string, restConfig *rest.Config) (*action.Configuration, error) {
	cfg := &action.Configuration{}
	rcg := &restClientGetter{
		config:    restConfig,
		namespace: namespace,
	}
	err := cfg.Init(rcg, namespace, c.helmDriver, c.debugFuncf)
	if err != nil {
		return nil, fmt.Errorf("helm action config init: %w", err)
	}
	return cfg, nil
}

func (c *configurationGetter) debugFuncf(format string, v ...interface{}) {
	if c.debug {
		c.log.Debug(fmt.Sprintf(format, v...))
	}
}

func mergeValuesOverrides(values map[string]interface{}, overrides map[string]string) error {
	for k, v := range overrides {
		value := fmt.Sprintf("%s=%v", k, v)
		if err := strvals.ParseInto(value, values); err != nil {
			return fmt.Errorf("parsing value=%s: %w", value, err)
		}
	}
	return nil
}

type restClientGetter struct {
	config    *rest.Config
	namespace string
}

func (r *restClientGetter) ToRESTConfig() (*rest.Config, error) {
	return r.config, nil
}

func (r *restClientGetter) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	// The more groups you have, the more discovery requests you need to make.
	// given 25 groups (our groups + a few custom resources) with one-ish version each, discovery needs to make 50 requests
	// double it just so we don't end up here again for a while.  This config is only used for discovery.
	config := *r.config
	config.Burst = 100

	clientset, err := kubernetes.NewForConfig(&config)
	if err != nil {
		return nil, err
	}
	return memorycached.NewMemCacheClient(clientset.Discovery()), nil
}

func (r *restClientGetter) ToRESTMapper() (meta.RESTMapper, error) {
	discoveryClient, err := r.ToDiscoveryClient()
	if err != nil {
		return nil, err
	}

	mapper := restmapper.NewDeferredDiscoveryRESTMapper(discoveryClient)
	expander := restmapper.NewShortcutExpander(mapper, discoveryClient)
	return expander, nil
}

func (r *restClientGetter) ToRawKubeConfigLoader() clientcmd.ClientConfig {
	// This method is unused in helm client implementation.
	return nil
}
