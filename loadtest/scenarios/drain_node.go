package scenarios

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/samber/lo"
	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

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

	nodesToDrain []*corev1.Node
}

func (s *drainNodeScenario) Name() string {
	return "drain node"
}

func (s *drainNodeScenario) Preparation(ctx context.Context, namespace string, clientset kubernetes.Interface) error {
	s.nodesToDrain = make([]*corev1.Node, 0, s.nodeCount)

	var lock sync.Mutex
	errGroup, ctx := errgroup.WithContext(ctx)

	for i := range s.nodeCount {
		errGroup.Go(func() error {
			nodeName := fmt.Sprintf("kwok-drain-%d", i)
			s.log.Info(fmt.Sprintf("Creating node %s", nodeName))
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

			s.log.Info(fmt.Sprintf("Creating deployment on node %s", nodeName))
			deployment := Deployment(fmt.Sprintf("fake-deployment-%s-%d", node.Name, i))
			deployment.ObjectMeta.Namespace = namespace
			//nolint:gosec // Not afraid of overflow here.
			deployment.Spec.Replicas = lo.ToPtr(int32(s.deploymentReplicas))
			deployment.Spec.Template.Spec.NodeName = nodeName

			_, err = clientset.AppsV1().Deployments(namespace).Create(ctx, deployment, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("failed to create fake deployment: %w", err)
			}

			// Wait for deployment to become ready, otherwise we might start draining before the pod is up.
			progressed := WaitUntil(ctx, 600*time.Second, func(ctx context.Context) bool {
				d, err := clientset.AppsV1().Deployments(namespace).Get(ctx, deployment.Name, metav1.GetOptions{})
				if err != nil {
					s.log.Warn("failed to get deployment after creating", "err", err)
					return false
				}
				return d.Status.ReadyReplicas == *d.Spec.Replicas
			})
			if !progressed {
				return fmt.Errorf("deployment %s did not progress to ready state in time", deployment.Name)
			}

			return nil
		})
	}

	return errGroup.Wait()
}

func (s *drainNodeScenario) Cleanup(ctx context.Context, namespace string, clientset kubernetes.Interface) error {
	var lock sync.Mutex
	var errs []error
	var wg sync.WaitGroup

	wg.Add(len(s.nodesToDrain))
	// We iterate through all nodes as they are not deleted with the ns and can leak => so we want to delete as many as possible.
	for _, n := range s.nodesToDrain {
		go func() {
			defer wg.Done()

			s.log.Info(fmt.Sprintf("Deleting node %s", n.Name))
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
			s.log.Info(fmt.Sprintf("Deleting deployment %s", deployment.Name))
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

	s.log.Info("Finished up cleaning nodes and deployments for drain.")
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
				NodeID:              "",
				DrainTimeoutSeconds: 60,
				Force:               true,
			},
		})
	}

	executor.ExecuteActions(ctx, actions)

	return nil
}
