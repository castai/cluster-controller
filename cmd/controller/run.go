package controller

import (
	"context"
	"fmt"
	"net/http"
	"net/http/pprof"
	"os"
	"strings"
	"time"

	"github.com/bombsimon/logrusr/v4"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apiserver/pkg/server/healthz"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/client-go/util/flowcontrol"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/castai/cluster-controller/cmd/utils"
	"github.com/castai/cluster-controller/health"
	"github.com/castai/cluster-controller/internal/actions"
	"github.com/castai/cluster-controller/internal/actions/csr"
	"github.com/castai/cluster-controller/internal/castai"
	"github.com/castai/cluster-controller/internal/config"
	"github.com/castai/cluster-controller/internal/controller"
	"github.com/castai/cluster-controller/internal/controller/logexporter"
	"github.com/castai/cluster-controller/internal/controller/metricexporter"
	"github.com/castai/cluster-controller/internal/helm"
	"github.com/castai/cluster-controller/internal/k8sversion"
	"github.com/castai/cluster-controller/internal/metrics"
	"github.com/castai/cluster-controller/internal/monitor"
	"github.com/castai/cluster-controller/internal/waitext"
)

const (
	maxRequestTimeout = 5 * time.Minute
)

func run(ctx context.Context) error {
	log := logrus.WithFields(logrus.Fields{})
	cfg := config.Get()

	binVersion := ctx.Value(utils.ClusterControllerVersionKey).(*config.ClusterControllerVersion)
	log.Infof("running castai-cluster-controller version %v", binVersion)

	logger := logexporter.NewLogger(cfg.Log.Level)

	cl, err := castai.NewRestyClient(cfg.API.URL, cfg.API.Key, cfg.TLS.CACert, logger.Level, binVersion, maxRequestTimeout)
	if err != nil {
		log.Fatalf("failed to create castai client: %v", err)
	}

	client := castai.NewClient(logger, cl, cfg.ClusterID, cfg.SelfPod.Name)

	logexporter.SetupLogExporter(logger, client)

	return runController(ctx, client, logger.WithFields(logrus.Fields{
		"cluster_id":           cfg.ClusterID,
		"version":              binVersion.String(),
		"autoscaling_disabled": cfg.AutoscalingDisabled,
	}), cfg, binVersion)
}

func runController(
	ctx context.Context,
	client castai.CastAIClient,
	logger *logrus.Entry,
	cfg config.Config,
	binVersion *config.ClusterControllerVersion,
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

	k8sVer, err := k8sversion.Get(clientset)
	if err != nil {
		return fmt.Errorf("getting kubernetes version: %w", err)
	}

	log := logger.WithFields(logrus.Fields{
		"version":       binVersion.Version,
		"k8s_version":   k8sVer.Full(),
		"running_on":    cfg.SelfPod.Node,
		"ctrl_pod_name": cfg.SelfPod.Name,
	})

	// Set logr/klog to logrus adapter so all logging goes through logrus
	logr := logrusr.New(log)
	klog.SetLogger(logr)

	log.Infof("running castai-cluster-controller version %v, log-level: %v", binVersion, logger.Level)

	// Setup cached client for DrainNodeHandler if VA wait feature is enabled
	var cachedClient ctrlclient.Client
	if cfg.Drain.WaitForVolumeDetach {
		cachedClient = setupCachedClient(ctx, log, restConfig, cfg.Drain.CacheSyncTimeout)
		if cachedClient == nil {
			log.Warn("cached client not available, VA wait feature will be disabled")
		}
	}

	actionHandlers := actions.NewDefaultActionHandlers(
		k8sVer.Full(),
		cfg.SelfPod.Namespace,
		log,
		clientset,
		dynamicClient,
		helmClient,
		actions.DrainConfig{
			WaitForVolumeDetach: cfg.Drain.WaitForVolumeDetach && cachedClient != nil,
			VolumeDetachTimeout: cfg.Drain.VolumeDetachTimeout,
			CachedClient:        cachedClient,
		},
	)

	actionsConfig := controller.Config{
		PollWaitInterval:     5 * time.Second,
		PollTimeout:          maxRequestTimeout,
		AckTimeout:           30 * time.Second,
		AckRetriesCount:      3,
		AckRetryWait:         1 * time.Second,
		ClusterID:            cfg.ClusterID,
		Version:              binVersion.Version,
		Namespace:            cfg.SelfPod.Namespace,
		MaxActionsInProgress: cfg.MaxActionsInProgress,
	}
	healthzAction := health.NewHealthzProvider(health.HealthzCfg{HealthyPollIntervalLimit: (actionsConfig.PollWaitInterval + actionsConfig.PollTimeout) * 2, StartTimeLimit: 2 * time.Minute}, log)

	svc := controller.NewService(
		log,
		actionsConfig,
		k8sVer.Full(),
		client,
		healthzAction,
		actionHandlers,
	)
	defer func() {
		if err := svc.Close(); err != nil {
			log.Errorf("failed to close controller service: %v", err)
		}
	}()

	if cfg.Metrics.ExportEnabled {
		metricExporter := metricexporter.New(log, client, cfg.Metrics.ExportInterval)
		go metricExporter.Run(ctx)
	}

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

		// https://deepsource.com/directory/go/issues/GO-S2114
		// => This is not a public API and runs in customer cluster; risk should be OK.
		//nolint:gosec
		if err := http.ListenAndServe(addr, httpMux); err != nil {
			log.Errorf("failed to start pprof http server: %v", err)
		}
	}()

	// Start http server for metrics
	go func() {
		addr := fmt.Sprintf(":%d", cfg.Metrics.Port)
		log.Infof("starting metrics on %s", addr)

		metrics.RegisterCustomMetrics()
		metricsMux := metrics.NewMetricsMux()
		// https://deepsource.com/directory/go/issues/GO-S2114
		// => This is not a public API and runs in customer cluster; risk should be OK.
		//nolint:gosec
		if err := http.ListenAndServe(addr, metricsMux); err != nil {
			log.Errorf("failed to start metrics http server: %v", err)
		}
	}()

	if err := saveMetadata(cfg.ClusterID, cfg, log); err != nil {
		return err
	}

	runSvc := func(ctx context.Context) {
		isGKE, err := runningOnGKE(clientset, cfg)
		if err != nil {
			log.Fatalf("failed to determine if running on GKE: %v", err)
		}

		log.Infof("Running on GKE is: %v", isGKE)

		if isGKE && !cfg.AutoscalingDisabled {
			csrMgr := csr.NewApprovalManager(log, clientset)
			if err := csrMgr.Start(ctx); err != nil {
				log.WithError(err).Fatal("failed to start approval manager")
			}

			log.Info("auto approve csr started as running on GKE")
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
				// So we need to check what whas reason of actually stopping.
				if err := ctx.Err(); err != nil {
					log.Infof("main context done, stopping controller: %v", err)
					return
				}
				log.Infof("leader lost: %s", id)
				// We don't need to exit here.
				// Leader "on started leading" receive a context that gets canceled when you're no longer the leader.
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

func runningOnGKE(clientset *kubernetes.Clientset, cfg config.Config) (bool, error) {
	// When running locally, there is no node.
	if cfg.SelfPod.Node == "" {
		return false, nil
	}

	var isGKE bool
	err := waitext.Retry(context.Background(), waitext.DefaultExponentialBackoff(), 3, func(ctx context.Context) (bool, error) {
		node, err := clientset.CoreV1().Nodes().Get(ctx, cfg.SelfPod.Node, metav1.GetOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
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

	return isGKE, err
}

func saveMetadata(clusterID string, cfg config.Config, log *logrus.Entry) error {
	metadata := monitor.Metadata{
		ClusterID: clusterID,
		LastStart: time.Now().UnixNano(),
	}
	log.Infof("saving metadata: %v to file: %v", metadata, cfg.MonitorMetadataPath)
	if err := metadata.Save(cfg.MonitorMetadataPath); err != nil {
		return fmt.Errorf("saving metadata: %w", err)
	}
	return nil
}

// setupCachedClient creates a controller-runtime cached client for efficient VolumeAttachment queries.
// Returns nil if cache setup fails (feature should be disabled gracefully).
func setupCachedClient(ctx context.Context, log logrus.FieldLogger, restConfig *rest.Config, syncTimeout time.Duration) ctrlclient.Client {
	// Create cache
	c, err := cache.New(restConfig, cache.Options{})
	if err != nil {
		log.Warnf("failed to create cache: %v", err)
		return nil
	}

	// Add field index for VolumeAttachments by nodeName
	if err := c.IndexField(ctx, &storagev1.VolumeAttachment{}, "spec.nodeName",
		func(obj ctrlclient.Object) []string {
			va := obj.(*storagev1.VolumeAttachment)
			return []string{va.Spec.NodeName}
		}); err != nil {
		log.Warnf("failed to add VolumeAttachment index: %v", err)
		return nil
	}

	// Add field index for Pods by nodeName
	if err := c.IndexField(ctx, &corev1.Pod{}, "spec.nodeName",
		func(obj ctrlclient.Object) []string {
			pod := obj.(*corev1.Pod)
			if pod.Spec.NodeName == "" {
				return nil
			}
			return []string{pod.Spec.NodeName}
		}); err != nil {
		log.Warnf("failed to add Pod index: %v", err)
		return nil
	}

	// Start cache in background
	go func() {
		if err := c.Start(ctx); err != nil {
			log.Errorf("cache stopped unexpectedly: %v", err)
		}
	}()

	// Wait for cache sync with timeout
	syncCtx, syncCancel := context.WithTimeout(ctx, syncTimeout)
	defer syncCancel()

	if !c.WaitForCacheSync(syncCtx) {
		log.Warnf("cache sync timed out after %v", syncTimeout)
		return nil
	}

	// Create client backed by cache
	cachedClient, err := ctrlclient.New(restConfig, ctrlclient.Options{
		Cache: &ctrlclient.CacheOptions{Reader: c},
	})
	if err != nil {
		log.Warnf("failed to create cached client: %v", err)
		return nil
	}

	log.Info("cache synced successfully, VA wait feature enabled with cached client")
	return cachedClient
}
