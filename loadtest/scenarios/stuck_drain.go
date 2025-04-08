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

func StuckDrain(nodeCount, deploymentReplicas int, log *slog.Logger) TestScenario {
	return &stuckDrainScenario{
		nodeCount:          nodeCount,
		deploymentReplicas: deploymentReplicas,
		log:                log,
	}
}

type stuckDrainScenario struct {
	nodeCount          int
	deploymentReplicas int
	log                *slog.Logger

	nodesToDrain []*corev1.Node
}

func (s *stuckDrainScenario) Name() string {
	return "drain node with stuck pdb"
}

func (s *stuckDrainScenario) Preparation(ctx context.Context, namespace string, clientset kubernetes.Interface) error {
	s.nodesToDrain = make([]*corev1.Node, 0, s.nodeCount)

	var lock sync.Mutex
	errGroup, ctx := errgroup.WithContext(ctx)

	for i := range s.nodeCount {
		errGroup.Go(func() error {
			nodeName := fmt.Sprintf("kwok-stuck-drain-%d", i)
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
			deployment, pdb := DeploymentWithStuckPDB(fmt.Sprintf("fake-deployment-%s-%d", node.Name, i))
			deployment.ObjectMeta.Namespace = namespace
			//nolint:gosec // Not afraid of overflow here.
			deployment.Spec.Replicas = lo.ToPtr(int32(s.deploymentReplicas))
			deployment.Spec.Template.Spec.NodeName = nodeName
			pdb.ObjectMeta.Namespace = namespace

			_, err = clientset.AppsV1().Deployments(namespace).Create(ctx, deployment, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("failed to create fake deployment: %w", err)
			}

			_, err = clientset.PolicyV1().PodDisruptionBudgets(namespace).Create(ctx, pdb, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("failed to create fake pod disruption budget: %w", err)
			}

			// Wait for deployment to become ready, otherwise we might start draining before the pod is up.
			progressed := WaitUntil(ctx, 120*time.Second, func(ctx context.Context) bool {
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

func (s *stuckDrainScenario) Cleanup(ctx context.Context, namespace string, clientset kubernetes.Interface) error {
	var errs []error
	// We iterate through all nodes as they are not deleted with the ns and can leak => so we want do delete as many as possible.
	for _, n := range s.nodesToDrain {
		s.log.Info(fmt.Sprintf("Deleting node %s", n.Name))
		err := clientset.CoreV1().Nodes().Delete(ctx, n.Name, metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			s.log.Warn("failed to delete fake node, will continue with other nodes", "nodeName", n.Name)
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	// We assume no other tests are using the same NS so just delete all.
	deploymentsInNS, err := clientset.AppsV1().Deployments(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list deployments: %w", err)
	}

	for _, deployment := range deploymentsInNS.Items {
		s.log.Info(fmt.Sprintf("Deleting deployment %s", deployment.Name))
		err = clientset.AppsV1().Deployments(namespace).Delete(ctx, deployment.Name, metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete fake deployment: %w", err)
		}
	}

	pdbsInNS, err := clientset.PolicyV1().PodDisruptionBudgets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list pod disruption budgets: %w", err)
	}

	for _, pdb := range pdbsInNS.Items {
		s.log.Info(fmt.Sprintf("Deleting PDB %s", pdb.Name))
		err = clientset.PolicyV1().PodDisruptionBudgets(namespace).Delete(ctx, pdb.Name, metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to delete fake pod disruption budget: %w", err)
		}
	}

	s.log.Info("Finished up cleaning nodes for drain, deployments and PDBs.")
	return nil
}

func (s *stuckDrainScenario) Run(ctx context.Context, _ string, _ kubernetes.Interface, executor ActionExecutor) error {
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
				Force:               false,
			},
		})
	}

	executor.ExecuteActions(ctx, actions)

	return nil
}
