package actions

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8s_types "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/record"

	"github.com/castai/cluster-controller/internal/castai"
)

func TestCreateEvent(t *testing.T) {
	r := require.New(t)
	id := k8s_types.UID(uuid.New().String())

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
			recorder := record.NewFakeRecorder(test.actionCount)
			broadCaster := record.NewBroadcasterForTests(time.Second * 10)
			h := CreateEventHandler{
				log:       logrus.New(),
				clientSet: clientSet,
				eventNsRecorder: map[string]record.EventRecorder{
					"castai": recorder,
				},
				eventNsBroadcaster: map[string]record.EventBroadcaster{
					"castai": broadCaster,
				},
			}
			ctx := context.Background()
			wg := sync.WaitGroup{}
			wg.Add(test.actionCount)
			for i := 0; i < test.actionCount; i++ {
				go func() {
					err := h.Handle(ctx, test.action)
					r.NoError(err)
					wg.Done()
				}()
			}
			wg.Wait()
			events := make([]string, 0, test.actionCount)
			for i := 0; i < test.actionCount; i++ {
				select {
				case event := <-recorder.Events:
					events = append(events, event)
				default:
					t.Errorf("not enough events expected %d actual %d", test.actionCount, i)
					continue
				}
			}
			for i := 0; i < test.actionCount; i++ {
				r.Contains(events[i], test.expectedEvent.Reason)
				r.Contains(events[i], test.expectedEvent.Message)
			}
			broadCaster.Shutdown()
		})
	}
}

func TestRandomNs(t *testing.T) {
	t.Parallel()
	r := require.New(t)
	actionCount := 10
	clientSet := fake.NewSimpleClientset(testPod(k8s_types.UID(uuid.New().String())))
	recorders := make([]*record.FakeRecorder, 0, actionCount)
	h := CreateEventHandler{
		log:       logrus.New(),
		clientSet: clientSet,
		recorderFactory: func(ns, reporter string) (record.EventBroadcaster, record.EventRecorder) {
			broadcaster := record.NewBroadcasterForTests(time.Second * 10)
			rec := record.NewFakeRecorder(actionCount)
			recorders = append(recorders, rec)
			return broadcaster, rec
		},
		eventNsRecorder:    map[string]record.EventRecorder{},
		eventNsBroadcaster: map[string]record.EventBroadcaster{},
	}
	ctx := context.Background()
	wg := sync.WaitGroup{}
	wg.Add(actionCount)
	for i := 0; i < actionCount; i++ {
		go func() {
			err := h.Handle(ctx, &castai.ClusterAction{
				ID: uuid.New().String(),
				ActionCreateEvent: &castai.ActionCreateEvent{
					ObjectRef: podObjReference(
						&corev1.Pod{
							ObjectMeta: metav1.ObjectMeta{
								Name:      fmt.Sprintf("testPod-%s", uuid.NewString()[:4]),
								Namespace: uuid.NewString(),
							},
						}),
					Reporter:  "provisioning.cast.ai",
					EventTime: time.Now(),
					EventType: "Warning",
					Reason:    "Just because!",
					Action:    "During node creation.",
					Message:   "Oh common, you can do better.",
				},
			})
			r.NoError(err)
			wg.Done()
		}()
	}
	wg.Wait()
	events := make([]string, 0, actionCount)
	for i := range recorders {
		select {
		case event := <-recorders[i].Events:
			events = append(events, event)
		default:
			t.Errorf("not enough events expected %d actual %d", actionCount, i)
			continue
		}
	}
	for ns, broadCaster := range h.eventNsBroadcaster {
		t.Logf("shutting down broadcaster for ns %s", ns)
		broadCaster.Shutdown()
	}
	r.Len(events, actionCount)
	for i := 0; i < actionCount; i++ {
		r.Contains(events[i], "Warning")
		r.Contains(events[i], "Oh common, you can do better.")
	}
}

func testPod(id k8s_types.UID) *corev1.Pod {
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
