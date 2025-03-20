package scenarios

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"

	"github.com/castai/cluster-controller/internal/castai"
)

func PodEvents(count int, log *slog.Logger) TestScenario {
	return &podEventsScenario{
		totalEvents: count,
		log:         log,
	}
}

type podEventsScenario struct {
	totalEvents int
	log         *slog.Logger
}

func (p *podEventsScenario) Name() string {
	return "pod events"
}

func (p *podEventsScenario) Preparation(ctx context.Context, namespace string, clientset kubernetes.Interface) error {
	// nothing to prepare for this test, pod does not have to exist to create events.
	return nil
}

func (p *podEventsScenario) Cleanup(ctx context.Context, namespace string, clientset kubernetes.Interface) error {
	// nothing to clean for this test, events are dropped automatically after certain time.

	return nil
}

func (p *podEventsScenario) Run(ctx context.Context, namespace string, _ kubernetes.Interface, executor ActionExecutor) error {
	p.log.Info(fmt.Sprintf("Starting creating %d events for different pods", p.totalEvents))
	actions := make([]castai.ClusterAction, 0, p.totalEvents)
	for i := range p.totalEvents {
		actions = append(actions, castai.ClusterAction{
			ID: uuid.NewString(),
			ActionCreateEvent: &castai.ActionCreateEvent{
				Reporter: "provisioning.cast.ai",
				ObjectRef: corev1.ObjectReference{
					Kind: "Pod",
					// Actions are executed async on CC, meaning they are acked even if rejected by server.
					// This means we can't rely on the test namespace as it'll disappear before all events are processed.
					// So we use a namespace that _will_ be there.
					Namespace:  corev1.NamespaceDefault,
					Name:       "Dummy-pod",
					UID:        types.UID(uuid.New().String()),
					APIVersion: "v1",
				},
				EventTime: time.Now(),
				EventType: "Warning",
				// Reason is different so events won't be aggregated by CC's event broadcaster.
				Reason:  fmt.Sprintf("Just because! %d", i),
				Action:  "During node creation.",
				Message: "Oh common, you can do better.",
			},
		})
	}
	executor.ExecuteActions(ctx, actions)

	return nil
}
