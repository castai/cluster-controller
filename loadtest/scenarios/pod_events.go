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
	return func() (Preparation, Cleanup, TestRun) {
		prepare := func(ctx context.Context, namespace string, clientset kubernetes.Interface) error {
			// nothing to prepare for this test, pod does not have to exist to create events.
			return nil
		}

		cleanup := func(ctx context.Context, namespace string, clientset kubernetes.Interface) error {
			// nothing to clean for this test, events are dropped automatically after certain time.

			return nil
		}

		run := func(_ context.Context, actionChannel chan<- castai.ClusterAction) error {
			log.Info(fmt.Sprintf("Starting creating %d events for different pods", count))
			for i := range count {
				actionChannel <- castai.ClusterAction{
					ID: uuid.NewString(),
					ActionCreateEvent: &castai.ActionCreateEvent{
						Reporter: "provisioning.cast.ai",
						ObjectRef: corev1.ObjectReference{
							Kind:       "Pod",
							Namespace:  "default",
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
				}
			}

			return nil
		}
		return prepare, cleanup, run
	}
}
