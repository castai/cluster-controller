package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/pprof"
	"os"
	"path"
	"runtime"
	"strings"
	"time"

	"github.com/bombsimon/logrusr/v4"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/net"
	"k8s.io/apiserver/pkg/server/healthz"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/client-go/util/flowcontrol"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"

	"github.com/castai/cluster-controller/health"
	"github.com/castai/cluster-controller/internal/actions/csr"
	"github.com/castai/cluster-controller/internal/castai"
	config2 "github.com/castai/cluster-controller/internal/config"
	"github.com/castai/cluster-controller/internal/controller"
	helm2 "github.com/castai/cluster-controller/internal/helm"
	"github.com/castai/cluster-controller/internal/k8sversion"
	"github.com/castai/cluster-controller/internal/logexporter"
	"github.com/castai/cluster-controller/internal/waitext"
)

// These should be set via `go build` during a release.
var (
	GitCommit = "undefined"
	GitRef    = "no-ref"
	Version   = "local"
)

const (
	maxRequestTimeout = 5 * time.Minute
)

func main() {
	log := logrus.WithFields(logrus.Fields{})
	cfg := config2.Get()

	binVersion := &config2.ClusterControllerVersion{
		GitCommit: GitCommit,
		GitRef:    GitRef,
		Version:   Version,
	}
	log.Infof("running castai-cluster-controller version %v", binVersion)

	logger := logrus.New()
	logger.SetLevel(logrus.Level(cfg.Log.Level))
	logger.SetReportCaller(true)
	logger.Formatter = &logrus.TextFormatter{
		CallerPrettyfier: func(f *runtime.Frame) (string, string) {
			filename := path.Base(f.File)
			return fmt.Sprintf("%s()", f.Function), fmt.Sprintf("%s:%d", filename, f.Line)
		},
	}
	cl, err := castai.NewRestyClient(cfg.API.URL, cfg.API.Key, cfg.TLS.CACert, logger.Level, binVersion, maxRequestTimeout)
	if err != nil {
		log.Fatalf("failed to create castai client: %v", err)
	}
	client := castai.NewClient(logger, cl, cfg.ClusterID)

	e := logexporter.NewLogExporter(logger, client)
	logger.AddHook(e)
	logrus.RegisterExitHandler(e.Wait)

	ctx := signals.SetupSignalHandler()
	if err := run(ctx, client, logger, cfg, binVersion); err != nil {
		logErr := &logContextError{}
		if errors.As(err, &logErr) {
			log = logger.WithFields(logErr.fields)
		}
		log.Fatalf("cluster-controller failed: %v", err)
	}
}

func run(
	ctx context.Context,
	client castai.CastAIClient,
	logger *logrus.Logger,
	cfg config2.Config,
	binVersion *config2.ClusterControllerVersion,
) (reterr error) {
	fields := logrus.Fields{}

	defer func() {
		if reterr == nil {
			return
		}
		reterr = &logContextError{
			err:    reterr,
			fields: fields,
		}
	}()

	restconfig, err := retrieveKubeConfig(logger)
	if err != nil {
		return err
	}
	restConfigLeader := rest.CopyConfig(restconfig)
	restConfigDynamic := rest.CopyConfig(restconfig)

	restconfig.RateLimiter = flowcontrol.NewTokenBucketRateLimiter(float32(cfg.KubeClient.QPS), cfg.KubeClient.Burst)
	restConfigLeader.RateLimiter = flowcontrol.NewTokenBucketRateLimiter(float32(cfg.KubeClient.QPS), cfg.KubeClient.Burst)
	restConfigDynamic.RateLimiter = flowcontrol.NewTokenBucketRateLimiter(float32(cfg.KubeClient.QPS), cfg.KubeClient.Burst)

	helmClient := helm2.NewClient(logger, helm2.NewChartLoader(logger), restconfig)

	clientset, err := kubernetes.NewForConfig(restconfig)
	if err != nil {
		return err
	}
	clientSetLeader, err := kubernetes.NewForConfig(restConfigLeader)
	if err != nil {
		return err
	}

	dynamicClient, err := dynamic.NewForConfig(restConfigDynamic)
	if err != nil {
		return err
	}

	k8sVersion, err := k8sversion.Get(clientset)
	if err != nil {
		return fmt.Errorf("getting kubernetes version: %w", err)
	}

	log := logger.WithFields(logrus.Fields{
		"version":       binVersion.Version,
		"k8s_version":   k8sVersion.Full(),
		"running_on":    cfg.NodeName,
		"ctrl_pod_name": cfg.PodName,
	})

	// Set logr/klog to logrus adapter so all logging goes through logrus.
	logr := logrusr.New(log)
	klog.SetLogger(logr)

	log.Infof("running castai-cluster-controller version %v, log-level: %v", binVersion, logger.Level)

	actionsConfig := controller.Config{
		PollWaitInterval: 5 * time.Second,
		PollTimeout:      maxRequestTimeout,
		AckTimeout:       30 * time.Second,
		AckRetriesCount:  3,
		AckRetryWait:     1 * time.Second,
		ClusterID:        cfg.ClusterID,
		Version:          binVersion.Version,
		Namespace:        cfg.LeaderElection.Namespace,
	}
	healthzAction := health.NewHealthzProvider(health.HealthzCfg{HealthyPollIntervalLimit: (actionsConfig.PollWaitInterval + actionsConfig.PollTimeout) * 2, StartTimeLimit: 2 * time.Minute}, log)

	svc := controller.NewService(
		log,
		actionsConfig,
		k8sVersion.Full(),
		clientset,
		dynamicClient,
		client,
		helmClient,
		healthzAction,
	)
	defer func() {
		if err := svc.Close(); err != nil {
			log.Errorf("failed to close controller service: %v", err)
		}
	}()

	httpMux := http.NewServeMux()
	var checks []healthz.HealthChecker
	checks = append(checks, healthzAction)
	var leaderHealthCheck *leaderelection.HealthzAdaptor
	if cfg.LeaderElection.Enabled {
		leaderHealthCheck = leaderelection.NewLeaderHealthzAdaptor(time.Minute)
		checks = append(checks, leaderHealthCheck)
	}
	healthz.InstallHandler(httpMux, checks...)
	installPprofHandlers(httpMux)

	// Start http server for pprof and health checks handlers.
	go func() {
		addr := fmt.Sprintf(":%d", cfg.PprofPort)
		log.Infof("starting pprof server on %s", addr)

		//TODO: remove nolint when we have a proper solution for this
		//nolint:gosec
		if err := http.ListenAndServe(addr, httpMux); err != nil {
			log.Errorf("failed to start pprof http server: %v", err)
		}
	}()

	runSvc := func(ctx context.Context) {
		isGKE, err := runningOnGKE(clientset, cfg)
		if err != nil {
			log.Fatalf("failed to determine if running on GKE: %v", err)
		}

		log.Debugf("auto approve csr: %v, running on GKE: %v", cfg.AutoApproveCSR, isGKE)
		if cfg.AutoApproveCSR && isGKE {
			csrMgr := csr.NewApprovalManager(log, clientset)
			csrMgr.Start(ctx)
		}

		svc.Run(ctx)
	}

	if cfg.LeaderElection.Enabled {
		// Run actions service with leader election. Blocks.
		return runWithLeaderElection(ctx, log, clientSetLeader, leaderHealthCheck, cfg.LeaderElection, runSvc)
	}

	// Run action service. Blocks.
	runSvc(ctx)
	return nil
}

func runWithLeaderElection(
	ctx context.Context,
	log logrus.FieldLogger,
	clientset kubernetes.Interface,
	watchDog *leaderelection.HealthzAdaptor,
	cfg config2.LeaderElection,
	runFunc func(ctx context.Context),
) error {
	id, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("failed to determine hostname used in leader ID: %w", err)
	}
	id = id + "_" + uuid.New().String()

	// Start the leader election code loop.
	leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
		Lock: &resourcelock.LeaseLock{
			LeaseMeta: metav1.ObjectMeta{
				Name:      cfg.LockName,
				Namespace: cfg.Namespace,
			},
			Client: clientset.CoordinationV1(),
			LockConfig: resourcelock.ResourceLockConfig{
				Identity: id,
			},
		},
		// IMPORTANT: you MUST ensure that any code you have that
		// is protected by the lease must terminate **before**
		// you call cancel. Otherwise, you could have a background
		// loop still running and another process could
		// get elected before your background loop finished, violating
		// the stated goal of the lease.
		ReleaseOnCancel: true,
		LeaseDuration:   cfg.LeaseDuration,
		RenewDeadline:   cfg.LeaseRenewDeadline,
		RetryPeriod:     3 * time.Second,
		WatchDog:        watchDog,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				log.WithFields(logrus.Fields{
					"leaseDuration":      cfg.LeaseDuration.String(),
					"leaseRenewDuration": cfg.LeaseRenewDeadline.String(),
				}).Infof("leader elected: %s", id)
				runFunc(ctx)
			},
			OnStoppedLeading: func() {
				// This method is always called(even if it was not a leader):
				// - when controller shuts dow (for example because of SIGTERM)
				// - we actually lost leader
				// So we need to check what whas reason of acutally stopping.
				if err := ctx.Err(); err != nil {
					log.Infof("main context done, stopping controller: %v", err)
					return
				}
				log.Infof("leader lost: %s", id)
				// We don't need to exit here.
				// Leader "on started leading" receive a context that gets cancelled when you're no longer the leader.
			},
			OnNewLeader: func(identity string) {
				// We're notified when new leader elected.
				if identity == id {
					// I just got the lock.
					return
				}
				log.Infof("new leader elected: %s", identity)
			},
		},
	})
	return nil
}

func installPprofHandlers(mux *http.ServeMux) {
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
}

func kubeConfigFromEnv() (*rest.Config, error) {
	kubepath := config2.Get().Kubeconfig
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

type logContextError struct {
	err    error
	fields logrus.Fields
}

func (e *logContextError) Error() string {
	return e.err.Error()
}

func (e *logContextError) Unwrap() error {
	return e.err
}

func runningOnGKE(clientset *kubernetes.Clientset, cfg config2.Config) (isGKE bool, err error) {
	err = waitext.Retry(context.Background(), waitext.DefaultExponentialBackoff(), 3, func(ctx context.Context) (bool, error) {
		node, err := clientset.CoreV1().Nodes().Get(ctx, cfg.NodeName, metav1.GetOptions{})
		if err != nil {
			return true, fmt.Errorf("getting node: %w", err)
		}

		for k := range node.Labels {
			if strings.HasPrefix(k, "cloud.google.com/") {
				isGKE = true
				return false, nil
			}
		}

		return false, nil
	}, func(err error) {
	})

	return
}
