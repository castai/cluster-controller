package scenarios

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/samber/lo"
	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"

	"github.com/castai/cluster-controller/internal/castai"
)

// DrainNode simulates draining of nodes that passes successfully (as opposed to StuckDrain).
func DrainNode(nodeCount, deploymentReplicas int, log *slog.Logger) TestScenario {
	return &drainNodeScenario{
		nodeCount:          nodeCount,
		deploymentReplicas: deploymentReplicas,
		log:                log,
	}
}

type drainNodeScenario struct {
	nodeCount          int
	deploymentReplicas int
	log                *slog.Logger

	nodesToDrain  []*corev1.Node
	daemonSetName string
}

func (s *drainNodeScenario) Name() string {
	return "drain node"
}

func (s *drainNodeScenario) Preparation(ctx context.Context, namespace string, clientset kubernetes.Interface) error {
	s.nodesToDrain = make([]*corev1.Node, 0, s.nodeCount)

	s.log.Info("creating resources...")

	factory := informers.NewSharedInformerFactory(clientset, 24*time.Hour)

	deploymentInformer := factory.Apps().V1().Deployments()
	lister := deploymentInformer.Lister()

	factory.Start(ctx.Done())
	syncCtx, syncCancel := context.WithTimeout(ctx, 1*time.Minute)
	defer syncCancel()

	s.log.Info("waiting for deploymentInformer cache sync...")
	if !cache.WaitForCacheSync(syncCtx.Done(), deploymentInformer.Informer().HasSynced) {
		return fmt.Errorf("failed to sync deploymentInformer")
	}

	s.log.Info("deploymentInformer cache synced")

	ds := KwokDaemonSet("drain-node-ds", namespace)
	if _, err := clientset.AppsV1().DaemonSets(namespace).Create(ctx, ds, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("failed to create DaemonSet: %w", err)
	}
	s.daemonSetName = ds.Name

	var createdNodes, readyPods atomic.Int32

	progressCtx, progressCancel := context.WithCancel(ctx)
	defer progressCancel()
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-progressCtx.Done():
				return
			case <-ticker.C:
				n := int(createdNodes.Load())
				p := int(readyPods.Load())
				totalPods := s.nodeCount * s.deploymentReplicas
				s.log.Info("resource creation in progress",
					"nodes_created", n,
					"nodes_remaining", s.nodeCount-n,
					"nodes_total", s.nodeCount,
					"pods_ready", p,
					"pods_remaining", totalPods-p,
					"pods_total", totalPods,
				)
			}
		}
	}()

	var lock sync.Mutex
	errGroup, ctx := errgroup.WithContext(ctx)

	for i := range s.nodeCount {
		errGroup.Go(func() error {
			nodeName := fmt.Sprintf("kwok-drain-%d", i)
			node := NewKwokNode(KwokConfig{}, nodeName)

			_, err := clientset.CoreV1().Nodes().Create(ctx, node, metav1.CreateOptions{})
			if err != nil && !apierrors.IsAlreadyExists(err) {
				return fmt.Errorf("failed to create fake node: %w", err)
			}
			if err != nil && apierrors.IsAlreadyExists(err) {
				s.log.Warn("node already exists, will reuse but potential conflict between test runs", "nodeName", nodeName)
			}
			lock.Lock()
			s.nodesToDrain = append(s.nodesToDrain, node)
			lock.Unlock()
			createdNodes.Add(1)

			deployment := Deployment(fmt.Sprintf("fake-deployment-%s-%d", node.Name, i))
			deployment.Namespace = namespace
			//nolint:gosec // Not afraid of overflow here.
			deployment.Spec.Replicas = lo.ToPtr(int32(s.deploymentReplicas))

			_, err = clientset.AppsV1().Deployments(namespace).Create(ctx, deployment, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("failed to create fake deployment: %w", err)
			}

			// Wait for deployment to become ready, otherwise we might start draining before the pod is up.
			progressed := WaitUntil(ctx, 600*time.Second, func(ctx context.Context) bool {
				d, err := lister.Deployments(namespace).Get(deployment.Name)
				if err != nil {
					return false
				}
				return d.Status.ReadyReplicas == *d.Spec.Replicas
			})
			if !progressed {
				return fmt.Errorf("deployment %s did not progress to ready state in time", deployment.Name)
			}

			readyPods.Add(1)
			return nil
		})
	}

	err := errGroup.Wait()

	s.log.Info("resources created, going to start test")

	return err
}

func (s *drainNodeScenario) Cleanup(ctx context.Context, namespace string, clientset kubernetes.Interface) error {
	var lock sync.Mutex
	var errs []error
	var wg sync.WaitGroup

	s.log.Info("cleaning up resources...")

	if s.daemonSetName != "" {
		err := clientset.AppsV1().DaemonSets(namespace).Delete(ctx, s.daemonSetName, metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete DaemonSet: %w", err)
		}
	}

	wg.Add(len(s.nodesToDrain))
	// We iterate through all nodes as they are not deleted with the ns and can leak => so we want to delete as many as possible.
	for _, n := range s.nodesToDrain {
		go func() {
			defer wg.Done()

			err := clientset.CoreV1().Nodes().Delete(ctx, n.Name, metav1.DeleteOptions{})
			if err != nil && !apierrors.IsNotFound(err) {
				s.log.Warn("failed to delete fake node, will continue with other nodes", "nodeName", n.Name)
				lock.Lock()
				errs = append(errs, err)
				lock.Unlock()
			}
		}()
	}

	wg.Wait()

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	// We assume no other tests are using the same NS so just delete all.
	deploymentsInNS, err := clientset.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list deployments: %w", err)
	}

	wg.Add(len(deploymentsInNS.Items))
	for _, deployment := range deploymentsInNS.Items {
		go func() {
			defer wg.Done()
			err = clientset.AppsV1().Deployments(namespace).Delete(ctx, deployment.Name, metav1.DeleteOptions{})
			if err != nil && !apierrors.IsNotFound(err) {
				s.log.Warn(
					"failed to delete fake deployment, will continue with other deployments and rely on namespace cleanup",
					"deploymentName",
					deployment.Name,
				)
			}
		}()
	}
	wg.Wait()

	s.log.Info("finished up cleaning nodes and deployments for drain.")
	return nil
}

func (s *drainNodeScenario) Run(ctx context.Context, _ string, _ kubernetes.Interface, executor ActionExecutor) error {
	s.log.Info(fmt.Sprintf("Starting drain action creation with %d nodes", len(s.nodesToDrain)))

	actions := make([]castai.ClusterAction, 0, len(s.nodesToDrain))
	for _, node := range s.nodesToDrain {
		actions = append(actions, castai.ClusterAction{
			ID:        uuid.NewString(),
			CreatedAt: time.Now().UTC(),
			ActionDrainNode: &castai.ActionDrainNode{
				NodeName:            node.Name,
				ProviderId:          node.Name,
				NodeID:              node.Name,
				DrainTimeoutSeconds: 60 * 25,
				Force:               true,
			},
		})
	}

	executor.ExecuteActions(ctx, actions)

	return nil
}
