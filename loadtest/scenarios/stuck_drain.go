package scenarios

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/castai/cluster-controller/internal/castai"
)

func StuckDrain(nodeCount int, deploymentReplicas int, log *slog.Logger) TestScenario {
	return func() (Preparation, Cleanup, TestRun) {
		var nodesToDrain []*corev1.Node

		// Creates node per action + 1 deployment and PDB for each node.
		prepare := func(ctx context.Context, namespace string, clientset kubernetes.Interface) error {
			for i := range nodeCount {
				nodeName := fmt.Sprintf("kwok-stuck-drain-%d", i)
				log.Info(fmt.Sprintf("Creating node %s", nodeName))
				node := NewKwokNode(KwokConfig{}, nodeName)

				_, err := clientset.CoreV1().Nodes().Create(ctx, node, metav1.CreateOptions{})
				if err != nil && !apierrors.IsAlreadyExists(err) {
					return fmt.Errorf("failed to create fake node: %w", err)
				}
				if err != nil && apierrors.IsAlreadyExists(err) {
					log.Warn("node already exists, will reuse but potential conflict between test runs", "nodeName", nodeName)
				}
				nodesToDrain = append(nodesToDrain, node)

				deployment, pdb := DeploymentWithStuckPDB(fmt.Sprintf("fake-deployment-%s-%d", node.Name, i))
				deployment.ObjectMeta.Namespace = namespace
				deployment.Spec.Replicas = lo.ToPtr(int32(deploymentReplicas))
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
			}

			return nil
		}

		cleanup := func(ctx context.Context, namespace string, clientset kubernetes.Interface) error {
			var errs []error
			// We iterate through all nodes as they are not deleted with the ns and can leak => so we want do delete as many as possible.
			for _, n := range nodesToDrain {
				err := clientset.CoreV1().Nodes().Delete(ctx, n.Name, metav1.DeleteOptions{})
				if err != nil && !apierrors.IsNotFound(err) {
					log.Warn("failed to delete fake node, will continue with other nodes", "nodeName", n.Name)
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
				err = clientset.PolicyV1().PodDisruptionBudgets(namespace).Delete(ctx, pdb.Name, metav1.DeleteOptions{})
				if err != nil && !apierrors.IsNotFound(err) {
					return fmt.Errorf("failed to delete fake pod disruption budget: %w", err)
				}
			}

			log.Info("Finished up cleaning nodes for drain, deployments and PDBs.")
			return nil
		}

		run := func(ctx context.Context, actionChannel chan<- castai.ClusterAction) error {
			log.Info(fmt.Sprintf("Starting drain action creation with %d nodes", len(nodesToDrain)))
			for _, node := range nodesToDrain {
				actionChannel <- castai.ClusterAction{
					ID:        uuid.NewString(),
					CreatedAt: time.Now().UTC(),
					ActionDrainNode: &castai.ActionDrainNode{
						NodeName:            node.Name,
						NodeID:              "",
						DrainTimeoutSeconds: 60,
						Force:               false,
					},
				}
			}

			return nil
		}
		return prepare, cleanup, run
	}
}
