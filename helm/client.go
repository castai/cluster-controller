//go:generate mockgen -source ./client.go -destination ./mock/client.go . Client

package helm

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/kube"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/strvals"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/discovery"
	memorycached "k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
	"sigs.k8s.io/yaml"

	"github.com/castai/cluster-controller/castai"
)

// group/version/kind/namespace/name
var ignoredCharts = map[string]bool{
	"rbac.authorization.k8s.io/v1/ClusterRole//castai-evictor": true,
	"rbac.authorization.k8s.io/v1/ClusterRoleBinding//castai-evictor": true,
}

const (
	K8sVersionLabel = "app.kubernetes.io/version"
	HelmVersionLabel = "helm.sh/chart"
)

type InstallOptions struct {
	ChartSource     *castai.ChartSource
	Namespace       string
	CreateNamespace bool
	ReleaseName     string
	ValuesOverrides map[string]string
}

type UninstallOptions struct {
	Namespace   string
	ReleaseName string
}

type UpgradeOptions struct {
	ChartSource     *castai.ChartSource
	Release         *release.Release
	ValuesOverrides map[string]string
	MaxHistory      int
}

type GetReleaseOptions struct {
	Namespace   string
	ReleaseName string
}

type RollbackOptions struct {
	Namespace   string
	ReleaseName string
}

func NewClient(log logrus.FieldLogger, loader ChartLoader, restConfig *rest.Config) Client {
	return &client{
		log: log,
		configurationGetter: &configurationGetter{
			log:        log,
			debug:      false,
			helmDriver: "secrets",
			k8sConfig:  restConfig,
		},
		chartLoader: loader,
	}
}

type Client interface {
	Install(ctx context.Context, opts InstallOptions) (*release.Release, error)
	Uninstall(opts UninstallOptions) (*release.UninstallReleaseResponse, error)
	Upgrade(ctx context.Context, opts UpgradeOptions) (*release.Release, error)
	Rollback(opts RollbackOptions) error
	GetRelease(opts GetReleaseOptions) (*release.Release, error)
}

type client struct {
	log                 logrus.FieldLogger
	configurationGetter ConfigurationGetter
	chartLoader         ChartLoader
}

// LabelIgnoreHook prevents certain charts getting updated, if only their version labels have changed
type LabelIgnoreHook struct {
	cfg        *action.Configuration
	oldRelease *release.Release
}

func (l *LabelIgnoreHook) Run(renderedManifests *bytes.Buffer) (*bytes.Buffer, error) {
	b := bytes.NewBuffer(nil)

	newManifests, err := l.cfg.KubeClient.Build(renderedManifests, false)
	if err != nil {
		return nil, err
	}

	oldManifests, err := l.cfg.KubeClient.Build(strings.NewReader(l.oldRelease.Manifest), false)
	if err != nil {
		return nil, err
	}

	for _, r := range newManifests {
		u := r.Object.(*unstructured.Unstructured)

		gvk := r.Object.GetObjectKind().GroupVersionKind()
		key := fmt.Sprintf("%s/%s/%s/%s", gvk.GroupVersion().String(), gvk.Kind, r.Namespace, r.Name)

		if ignoredCharts[key] {
			name := u.GetName()
			kind := u.GetKind()
			namespace := u.GetNamespace()

			oldLabels := getChartLabels(oldManifests, name, kind, namespace)
			labelCopy := u.GetLabels()
			// Reset version to previous release
			labelCopy[K8sVersionLabel] = oldLabels[K8sVersionLabel]
			labelCopy[HelmVersionLabel] = oldLabels[HelmVersionLabel]
			u.SetLabels(labelCopy)
		}

		// Copy-pasted from kustomize
		js, err := u.MarshalJSON()
		if err != nil {
			return nil, err
		}

		y, err := yaml.JSONToYAML(js)
		if err != nil {
			return nil, err
		}

		fmt.Fprintf(b, "---\n%s\n", y)
	}

	return b, nil
}

func getChartLabels(list kube.ResourceList, chartName, kind, namespace string) map[string]string {
	for _, r := range list {
		u := r.Object.(*unstructured.Unstructured)

		// add namespace
		if u.GetName() == chartName && u.GetKind() == kind && u.GetNamespace() == namespace {
			return u.GetLabels()
		}
	}

	return nil
}

func (c *client) Install(ctx context.Context, opts InstallOptions) (*release.Release, error) {
	ch, err := c.chartLoader.Load(ctx, opts.ChartSource)
	if err != nil {
		return nil, err
	}

	if req := ch.Metadata.Dependencies; req != nil {
		if err := action.CheckDependencies(ch, req); err != nil {
			return nil, err
		}
	}

	namespace := opts.Namespace
	cfg, err := c.configurationGetter.Get(namespace)
	if err != nil {
		return nil, err
	}

	install := action.NewInstall(cfg)
	install.Namespace = namespace
	install.CreateNamespace = opts.CreateNamespace
	install.ReleaseName = opts.ReleaseName
	install.Timeout = 10 * time.Minute
	install.Wait = true // Wait unit all applied resources are running.

	// Prepare user value overrides.
	values := map[string]interface{}{}
	if err := mergeValuesOverrides(values, opts.ValuesOverrides); err != nil {
		return nil, err
	}

	res, err := install.Run(ch, values)
	if err != nil {
		return nil, fmt.Errorf("running chart install, name=%q: %w", ch.Name(), err)
	}
	return res, err
}

func (c *client) Uninstall(opts UninstallOptions) (*release.UninstallReleaseResponse, error) {
	cfg, err := c.configurationGetter.Get(opts.Namespace)
	if err != nil {
		return nil, err
	}

	uninstall := action.NewUninstall(cfg)
	res, err := uninstall.Run(opts.ReleaseName)
	if err != nil {
		return nil, fmt.Errorf("chart uninstall failed, name=%s, namespace=%s: %w", opts.ReleaseName, opts.Namespace, err)
	}
	return res, nil
}

func (c *client) Upgrade(ctx context.Context, opts UpgradeOptions) (*release.Release, error) {
	ch, err := c.chartLoader.Load(ctx, opts.ChartSource)
	if err != nil {
		return nil, err
	}

	if req := ch.Metadata.Dependencies; req != nil {
		if err := action.CheckDependencies(ch, req); err != nil {
			return nil, err
		}
	}

	namespace := opts.Release.Namespace
	cfg, err := c.configurationGetter.Get(namespace)
	if err != nil {
		return nil, err
	}

	upgrade := action.NewUpgrade(cfg)
	upgrade.Namespace = namespace
	upgrade.MaxHistory = opts.MaxHistory
	upgrade.PostRenderer = &LabelIgnoreHook{
		cfg: cfg,
		oldRelease: opts.Release,
	}
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

func (c *client) Rollback(opts RollbackOptions) error {
	cfg, err := c.configurationGetter.Get(opts.Namespace)
	if err != nil {
		return err
	}

	rollback := action.NewRollback(cfg)
	err = rollback.Run(opts.ReleaseName)
	if err != nil {
		return fmt.Errorf("chart rollback failed, name=%s, namespace=%s: %w", opts.ReleaseName, opts.Namespace, err)
	}
	return nil
}

func (c *client) GetRelease(opts GetReleaseOptions) (*release.Release, error) {
	cfg, err := c.configurationGetter.Get(opts.Namespace)
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
	Get(namespace string) (*action.Configuration, error)
}

type configurationGetter struct {
	log        logrus.FieldLogger
	debug      bool
	helmDriver string
	k8sConfig  *rest.Config
}

func (c *configurationGetter) Get(namespace string) (*action.Configuration, error) {
	cfg := &action.Configuration{}
	rcg := &restClientGetter{
		config:    c.k8sConfig,
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
	return &fakeClientConfig{
		config:    r.config,
		namespace: r.namespace,
	}
}

// fakeClientConfig is used to inject Helm required interface dependency. Helm uses only Namespace() method from ClientConfig.
// There's no straight forward way to build clientcmd.ClientConfig from k8s restConfig hence using fake one.
type fakeClientConfig struct {
	config    *rest.Config
	namespace string
}

func (f *fakeClientConfig) RawConfig() (api.Config, error) {
	return api.Config{}, nil
}

func (f *fakeClientConfig) ClientConfig() (*rest.Config, error) {
	return f.config, nil
}

func (f *fakeClientConfig) Namespace() (string, bool, error) {
	return f.namespace, f.namespace != "", nil
}

func (f *fakeClientConfig) ConfigAccess() clientcmd.ConfigAccess {
	return &clientcmd.PathOptions{}
}
