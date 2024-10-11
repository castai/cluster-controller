package controller

import (
	"context"
	"fmt"

	"github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes"

	"github.com/bombsimon/logrusr/v4"
	"github.com/castai/cluster-controller/actions"
	"github.com/castai/cluster-controller/csr"
	"github.com/castai/cluster-controller/health"
	"github.com/castai/cluster-controller/helm"
	"github.com/castai/cluster-controller/internal/castai"
	"github.com/castai/cluster-controller/internal/config"
	"github.com/castai/cluster-controller/internal/k8version"
	"github.com/castai/cluster-controller/waitext"
	"github.com/google/uuid"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/server/healthz"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/client-go/util/flowcontrol"
	"k8s.io/klog/v2"
	"net/http"
	"net/http/pprof"
	"os"
	"strings"
	"time"
)

const (
	maxRequestTimeout = 5 * time.Minute
)

func run(ctx context.Context) error {
	log := logrus.WithFields(logrus.Fields{})
	cfg := config.Get()

	binVersion := ctx.Value("agentVersion").(*config.ClusterControllerVersion)
	log.Infof("running castai-cluster-controller version %v", binVersion)

	logger := castai.NewLogger(uint32(cfg.Log.Level))

	cl, err := castai.NewRestyClient(cfg.API.URL, cfg.API.Key, cfg.TLS.CACert, logger.Level, binVersion, maxRequestTimeout)
	if err != nil {
		log.Fatalf("failed to create castai client: %v", err)

	}

	client := castai.NewClient(logger, cl, cfg.ClusterID)

	castai.SetupLogExporter(logger, client)

	return runController(ctx, client, logger.WithFields(logrus.Fields{
		"cluster_id": cfg.ClusterID,
		"version":    binVersion.String(),
	}), cfg, binVersion)
}

func runController(
	ctx context.Context,
	client castai.ActionsClient,
	logger *logrus.Entry,
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

	restConfig, err := config.RetrieveKubeConfig(logger)
	if err != nil {
		return err
	}
	restConfigLeader := rest.CopyConfig(restConfig)
	restConfigDynamic := rest.CopyConfig(restConfig)

	restConfig.RateLimiter = flowcontrol.NewTokenBucketRateLimiter(float32(cfg.KubeClient.QPS), cfg.KubeClient.Burst)
	restConfigLeader.RateLimiter = flowcontrol.NewTokenBucketRateLimiter(float32(cfg.KubeClient.QPS), cfg.KubeClient.Burst)
	restConfigDynamic.RateLimiter = flowcontrol.NewTokenBucketRateLimiter(float32(cfg.KubeClient.QPS), cfg.KubeClient.Burst)

	helmClient := helm.NewClient(logger, helm.NewChartLoader(logger), restConfig)

	clientset, err := kubernetes.NewForConfig(restConfig)
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

	k8sVersion, err := k8version.Get(clientset)
	if err != nil {
		return fmt.Errorf("getting kubernetes version: %w", err)
	}

	log := logger.WithFields(logrus.Fields{
		"version":       binVersion.Version,
		"k8s_version":   k8sVersion.Full(),
		"running_on":    cfg.SelfPod.Node,
		"ctrl_pod_name": cfg.SelfPod.Name,
	})

	// Set logr/klog to logrus adapter so all logging goes through logrus
	logr := logrusr.New(log)
	klog.SetLogger(logr)

	log.Infof("running castai-cluster-controller version %v, log-level: %v", binVersion, logger.Level)

	actionsConfig := actions.Config{
		PollWaitInterval: 5 * time.Second,
		PollTimeout:      maxRequestTimeout,
		AckTimeout:       30 * time.Second,
		AckRetriesCount:  3,
		AckRetryWait:     1 * time.Second,
		ClusterID:        cfg.ClusterID,
		Version:          binVersion.Version,
		Namespace:        cfg.SelfPod.Namespace,
	}
	healthzAction := health.NewHealthzProvider(health.HealthzCfg{HealthyPollIntervalLimit: (actionsConfig.PollWaitInterval + actionsConfig.PollTimeout) * 2, StartTimeLimit: 2 * time.Minute}, log)

	svc := actions.NewService(
		log,
		actionsConfig,
		k8sVersion.Full(),
		clientset,
		dynamicClient,
		client,
		helmClient,
		healthzAction,
	)

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
		return runWithLeaderElection(ctx, log, clientSetLeader, leaderHealthCheck, &cfg, runSvc)
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
	cfg *config.Config,
	runFunc func(ctx context.Context),
) error {
	id, err := os.Hostname()
	if err != nil {
		return fmt.Errorf("failed to determine hostname used in leader ID: %w", err)
	}
	id = id + "_" + uuid.New().String()

	// Start the leader election code loop
	leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
		Lock: &resourcelock.LeaseLock{
			LeaseMeta: metav1.ObjectMeta{
				Name:      cfg.LeaderElection.LockName,
				Namespace: cfg.SelfPod.Namespace,
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
		LeaseDuration:   cfg.LeaderElection.LeaseDuration,
		RenewDeadline:   cfg.LeaderElection.LeaseRenewDeadline,
		RetryPeriod:     3 * time.Second,
		WatchDog:        watchDog,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				log.WithFields(logrus.Fields{
					"leaseDuration":      cfg.LeaderElection.LeaseDuration.String(),
					"leaseRenewDuration": cfg.LeaderElection.LeaseRenewDeadline.String(),
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

func runningOnGKE(clientset *kubernetes.Clientset, cfg config.Config) (isGKE bool, err error) {
	err = waitext.Retry(context.Background(), waitext.DefaultExponentialBackoff(), 3, func(ctx context.Context) (bool, error) {
		node, err := clientset.CoreV1().Nodes().Get(ctx, cfg.SelfPod.Node, metav1.GetOptions{})
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
