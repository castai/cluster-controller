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

func PatchNode(nodeCount int, log *slog.Logger) TestScenario {
	return &patchNodeScenario{
		nodeCount: nodeCount,
		log:       log,
	}
}

type patchNodeScenario struct {
	nodeCount          int
	deploymentReplicas int
	log                *slog.Logger

	nodesToPatch []*corev1.Node
}

func (s *patchNodeScenario) Name() string {
	return "patch node"
}

func (s *patchNodeScenario) Preparation(ctx context.Context, namespace string, clientset kubernetes.Interface) error {
	s.nodesToPatch = make([]*corev1.Node, 0, s.nodeCount)

	var lock sync.Mutex
	errGroup, ctx := errgroup.WithContext(ctx)

	for i := range s.nodeCount {
		errGroup.Go(func() error {
			nodeName := fmt.Sprintf("kwok-patch-%d", i)
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
			s.nodesToPatch = append(s.nodesToPatch, node)
			lock.Unlock()

			return nil
		})
	}

	return errGroup.Wait()
}

func (s *patchNodeScenario) Cleanup(ctx context.Context, namespace string, clientset kubernetes.Interface) error {
	var lock sync.Mutex
	var errs []error
	var wg sync.WaitGroup

	wg.Add(len(s.nodesToPatch))
	// We iterate through all nodes as they are not deleted with the ns and can leak => so we want to delete as many as possible.
	for _, n := range s.nodesToPatch {
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

	s.log.Info("Finished up cleaning nodes for patching.")
	return nil
}

func (s *patchNodeScenario) Run(ctx context.Context, _ string, _ kubernetes.Interface, executor ActionExecutor) error {
	s.log.Info(fmt.Sprintf("Starting patch node action creation with %d nodes", len(s.nodesToPatch)))

	actions := make([]castai.ClusterAction, 0, len(s.nodesToPatch))
	for _, node := range s.nodesToPatch {
		actions = append(actions, castai.ClusterAction{
			ID:        uuid.NewString(),
			CreatedAt: time.Now().UTC(),
			ActionPatchNode: &castai.ActionPatchNode{
				NodeName:      node.Name,
				NodeID:        "",
				Labels:        map[string]string{"Test": "label"},
				Annotations:   map[string]string{"Test": "annotation"},
				Unschedulable: lo.ToPtr(true),
				Capacity:      nil,
			},
		})
	}

	executor.ExecuteActions(ctx, actions)

	return nil
}
