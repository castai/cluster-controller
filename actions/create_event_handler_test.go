package actions

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/castai/cluster-controller/castai"
)

func TestCreateEvent(t *testing.T) {
	r := require.New(t)
	id := types.UID(uuid.New().String())

	tests := []struct {
		name          string
		action        *castai.ClusterAction
		actionCount   int
		object        runtime.Object
		expectedEvent *corev1.Event
	}{
		{
			name: "create single pod event",
			action: &castai.ClusterAction{
				ID: uuid.New().String(),
				ActionCreateEvent: &castai.ActionCreateEvent{
					Reporter:  "autoscaler.cast.ai",
					ObjectRef: podObjReference(testPod(id)),
					EventTime: time.Now(),
					EventType: "Normal",
					Reason:    "Just because!",
					Action:    "During node creation.",
					Message:   "Oh common, you can do better.",
				},
			},
			actionCount: 1,
			object:      testPod(id),
			expectedEvent: &corev1.Event{
				ObjectMeta:          metav1.ObjectMeta{Namespace: "castai"},
				Type:                "Normal",
				Reason:              "Just because!",
				Action:              "During node creation.",
				Message:             "Oh common, you can do better.",
				ReportingController: "autoscaler.cast.ai",
				ReportingInstance:   "autoscaler.cast.ai",
			},
		},
		{
			name: "create several pod events",
			action: &castai.ClusterAction{
				ID: "",
				ActionCreateEvent: &castai.ActionCreateEvent{
					Reporter:  "provisioning.cast.ai",
					ObjectRef: podObjReference(testPod(id)),
					EventTime: time.Now(),
					EventType: "Warning",
					Reason:    "Just because!",
					Action:    "During node creation.",
					Message:   "Oh common, you can do better.",
				},
			},
			actionCount: 6,
			object:      testPod(id),
			expectedEvent: &corev1.Event{
				ObjectMeta:          metav1.ObjectMeta{Namespace: "castai"},
				Type:                "Warning",
				Reason:              "Just because!",
				Action:              "During node creation.",
				Message:             "Oh common, you can do better.",
				ReportingController: "provisioning.cast.ai",
				ReportingInstance:   "provisioning.cast.ai",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clientSet := fake.NewSimpleClientset(test.object)
			h := createEventHandler{
				log:       logrus.New(),
				clientset: clientSet,
			}

			ctx := context.Background()
			for i := 0; i < test.actionCount; i++ {
				err := h.Handle(ctx, test.action)
				r.NoError(err)
			}

			eventTime := test.action.ActionCreateEvent.EventTime
			testEventName := fmt.Sprintf("%v.%x", test.action.ActionCreateEvent.ObjectRef.Name, eventTime.Unix())

			testEvent, err := clientSet.CoreV1().
				Events(test.expectedEvent.Namespace).
				Get(ctx, testEventName, metav1.GetOptions{})
			r.NoError(err)

			r.Equal(test.expectedEvent.Type, testEvent.Type)
			r.Equal(test.expectedEvent.Reason, testEvent.Reason)
			r.Equal(test.expectedEvent.Action, testEvent.Action)
			r.Equal(test.expectedEvent.Message, testEvent.Message)
			r.Equal(test.expectedEvent.ReportingController, testEvent.ReportingController)
			r.Equal(test.expectedEvent.ReportingInstance, testEvent.ReportingInstance)
			r.EqualValues(test.actionCount, testEvent.Count)
		})
	}
}

func testPod(id types.UID) *corev1.Pod {
	return &corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Pod",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "testPod",
			UID:       id,
			Namespace: "castai",
		},
	}
}

func podObjReference(p *corev1.Pod) corev1.ObjectReference {
	return corev1.ObjectReference{
		Kind:            p.Kind,
		Namespace:       p.Namespace,
		Name:            p.Name,
		UID:             p.UID,
		APIVersion:      p.APIVersion,
		ResourceVersion: p.ResourceVersion,
	}
}
