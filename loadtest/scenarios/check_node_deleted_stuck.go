package scenarios

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/castai/cluster-controller/internal/castai"
)

// CheckNodeDeletedStuck simulates a case where the node is not deleted so the checker gets stuck.
func CheckNodeDeletedStuck(actionCount int, log *slog.Logger) TestScenario {
	return &checkNodeDeletedStuckScenario{
		actionCount: actionCount,
		log:         log,
	}
}

type checkNodeDeletedStuckScenario struct {
	actionCount int
	log         *slog.Logger

	nodes []*corev1.Node
}

func (s *checkNodeDeletedStuckScenario) Name() string {
	return "check node deleted"
}

func (s *checkNodeDeletedStuckScenario) Preparation(ctx context.Context, namespace string, clientset kubernetes.Interface) error {
	s.nodes = make([]*corev1.Node, 0, s.actionCount)

	var lock sync.Mutex
	errGroup, ctx := errgroup.WithContext(ctx)

	nodeCount := int(math.Ceil(float64(s.actionCount) / nodeTestsCountOptimizeFactor))

	for i := range nodeCount {
		errGroup.Go(func() error {
			nodeName := fmt.Sprintf("kwok-check-deleted-%d", i)
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
			s.nodes = append(s.nodes, node)
			lock.Unlock()

			return nil
		})
	}

	return errGroup.Wait()
}

func (s *checkNodeDeletedStuckScenario) Cleanup(ctx context.Context, namespace string, clientset kubernetes.Interface) error {
	var lock sync.Mutex
	var errs []error
	var wg sync.WaitGroup

	wg.Add(len(s.nodes))
	// We iterate through all nodes as they are not deleted with the ns and can leak => so we want to delete as many as possible.
	for _, n := range s.nodes {
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

	s.log.Info("Finished up cleaning nodes for status check.")
	return nil
}

func (s *checkNodeDeletedStuckScenario) Run(ctx context.Context, _ string, _ kubernetes.Interface, executor ActionExecutor) error {
	s.log.Info(fmt.Sprintf("Starting check node deleted action with %d nodes", len(s.nodes)))

	// Note: there is no code that should delete the node so each action should fail with timeout
	// -> this puts more load than "expected" to simulate such edge case.
	actions := make([]castai.ClusterAction, 0, s.actionCount)
	for i := range s.actionCount {
		node := s.nodes[i%len(s.nodes)]
		actions = append(actions, castai.ClusterAction{
			ID:        uuid.NewString(),
			CreatedAt: time.Now().UTC(),
			ActionCheckNodeDeleted: &castai.ActionCheckNodeDeleted{
				NodeName:   node.Name,
				ProviderId: node.Spec.ProviderID,
			},
		})
	}

	executor.ExecuteActions(ctx, actions)

	return nil
}
