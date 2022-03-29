package main

import (
	"context"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	_ "net/http/pprof"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"

	"github.com/castai/cluster-controller/helm"

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

	ctx := signals.SetupSignalHandler()
	if err := run(ctx, client, logger, cfg, binVersion); err != nil {
		logErr := &logContextErr{}
		if errors.As(err, &logErr) {
			log = logger.WithFields(logErr.fields)
		}
		log.Fatalf("cluster-controller failed: %v", err)
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
		return fmt.Errorf("getting kubernetes version: %w", err)
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

	if cfg.LeaderElection.Enabled {
		lock, err := newLeaseLock(clientset, cfg.LeaderElection.LockName, cfg.LeaderElection.Namespace)
		if err != nil {
			return err
		}
		// Run actions service with leader election. Blocks.
		runWithLeaderElection(ctx, log, lock, func(ctx context.Context) {
			svc.Run(ctx)
		})
		return nil
	}

	// Run action service. Blocks.
	svc.Run(ctx)
	return nil
}

func newLeaseLock(client kubernetes.Interface, lockName, lockNamespace string) (*resourcelock.LeaseLock, error) {
	id, err := os.Hostname()
	if err != nil {
		return nil, fmt.Errorf("failed to determine hostname used in leader ID: %w", err)
	}
	id = id + "_" + uuid.New().String()

	return &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      lockName,
			Namespace: lockNamespace,
		},
		Client: client.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: id,
		},
	}, nil
}

func runWithLeaderElection(ctx context.Context, log logrus.FieldLogger, lock *resourcelock.LeaseLock, runFunc func(ctx context.Context)) {
	// Start the leader election code loop
	leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
		Lock: lock,
		// IMPORTANT: you MUST ensure that any code you have that
		// is protected by the lease must terminate **before**
		// you call cancel. Otherwise, you could have a background
		// loop still running and another process could
		// get elected before your background loop finished, violating
		// the stated goal of the lease.
		ReleaseOnCancel: true,
		LeaseDuration:   60 * time.Second,
		RenewDeadline:   15 * time.Second,
		RetryPeriod:     5 * time.Second,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				log.Infof("started leader: %s", lock.Identity())
				runFunc(ctx)
			},
			OnStoppedLeading: func() {
				log.Infof("leader lost: %s", lock.Identity())
				os.Exit(0)
			},
			OnNewLeader: func(identity string) {
				// We're notified when new leader elected.
				if identity == lock.Identity() {
					// I just got the lock.
					return
				}
				log.Infof("new leader elected: %s", identity)
			},
		},
	})
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
