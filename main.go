package main

import (
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"time"

	"github.com/castai/cluster-controller/helm"
	"github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"

	"github.com/castai/cluster-controller/actions"
	"github.com/castai/cluster-controller/castai"
	"github.com/castai/cluster-controller/config"
	ctrlog "github.com/castai/cluster-controller/log"
	"github.com/castai/cluster-controller/version"
)

// These should be set via `go build` during a release.
var (
	GitCommit = "undefined"
	GitRef    = "no-ref"
	Version   = "local"
)

func main() {
	cfg := config.Get()

	binVersion := &config.ClusterControllerVersion{
		GitCommit: GitCommit,
		GitRef:    GitRef,
		Version:   Version,
	}

	logger := logrus.New()
	logger.SetLevel(logrus.Level(cfg.Log.Level))

	client := castai.NewClient(
		logger,
		castai.NewDefaultClient(cfg.API.URL, cfg.API.Key, logger.Level, binVersion),
		cfg.ClusterID,
	)

	log := logrus.WithFields(logrus.Fields{})
	if err := run(signals.SetupSignalHandler(), client, logger, cfg, binVersion); err != nil {
		logErr := &logContextErr{}
		if errors.As(err, &logErr) {
			log = logger.WithFields(logErr.fields)
		}
		log.Fatalf("agent-actions failed: %v", err)
	}
}

func run(
	ctx context.Context,
	client castai.Client,
	logger *logrus.Logger,
	cfg config.Config,
	binVersion *config.ClusterControllerVersion,
) (reterr error) {
	fields := logrus.Fields{}

	defer func() {
		if reterr == nil {
			return
		}
		reterr = &logContextErr{
			err:    reterr,
			fields: fields,
		}
	}()

	e := ctrlog.NewExporter(logger, client)
	logger.AddHook(e)
	logrus.RegisterExitHandler(e.Wait)

	restconfig, err := retrieveKubeConfig(logger)
	if err != nil {
		return err
	}

	helmClient := helm.NewClient(logger, helm.NewChartLoader(), restconfig)

	clientset, err := kubernetes.NewForConfig(restconfig)
	if err != nil {
		return err
	}

	k8sVersion, err := version.Get(clientset)
	if err != nil {
		panic(fmt.Errorf("failed getting kubernetes version: %v", err))
	}

	log := logger.WithFields(logrus.Fields{
		"version":     binVersion.Version,
		"k8s_version": k8sVersion.Full(),
	})
	log.Infof("running castai-cluster-controller version %v", binVersion)

	if cfg.PprofPort != 0 {
		go func() {
			addr := fmt.Sprintf(":%d", cfg.PprofPort)
			log.Infof("starting pprof server on %s", addr)
			if err := http.ListenAndServe(addr, http.DefaultServeMux); err != nil {
				log.Errorf("failed to start pprof http server: %v", err)
			}
		}()
	}

	actionsConfig := actions.Config{
		PollWaitInterval: 5 * time.Second,
		PollTimeout:      5 * time.Minute,
		AckTimeout:       30 * time.Second,
		AckRetriesCount:  3,
		AckRetryWait:     1 * time.Second,
		ClusterID:        cfg.ClusterID,
	}
	svc := actions.NewService(log, actionsConfig, clientset, client, helmClient)

	// Run action service. Blocks.
	if err := svc.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

func kubeConfigFromEnv() (*rest.Config, error) {
	kubepath := config.Get().Kubeconfig
	if kubepath == "" {
		return nil, nil
	}

	data, err := ioutil.ReadFile(kubepath)
	if err != nil {
		return nil, fmt.Errorf("reading kubeconfig at %s: %w", kubepath, err)
	}

	restConfig, err := clientcmd.RESTConfigFromKubeConfig(data)
	if err != nil {
		return nil, fmt.Errorf("building rest config from kubeconfig at %s: %w", kubepath, err)
	}

	return restConfig, nil
}

func retrieveKubeConfig(log logrus.FieldLogger) (*rest.Config, error) {
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
	log.Debug("using in cluster kubeconfig")
	return inClusterConfig, nil
}

type logContextErr struct {
	err    error
	fields logrus.Fields
}

func (e *logContextErr) Error() string {
	return e.err.Error()
}

func (e *logContextErr) Unwrap() error {
	return e.err
}
