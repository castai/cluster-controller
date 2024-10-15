package actions

import (
	"context"
	"fmt"
	"sync"

	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	typedv1core "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"

	"github.com/castai/cluster-controller/internal/types"
)

var _ types.ActionHandler = &CreateEventHandler{}

func NewCreateEventHandler(log logrus.FieldLogger, clientset kubernetes.Interface) *CreateEventHandler {
	factory := func(ns, reporter string) (record.EventBroadcaster, record.EventRecorder) {
		eventBroadcaster := record.NewBroadcaster()
		eventBroadcaster.StartRecordingToSink(&typedv1core.EventSinkImpl{Interface: clientset.CoreV1().Events(ns)})
		eventBroadcaster.StartStructuredLogging(0)
		log.Debug("create new broadcaster and recorder for namespace: %s", ns)
		// Create an event recorder
		return eventBroadcaster, eventBroadcaster.NewRecorder(nil, v1.EventSource{
			Component: reporter,
			Host:      reporter,
		})
	}
	return &CreateEventHandler{
		log:                log,
		clientSet:          clientset,
		recorderFactory:    factory,
		eventNsBroadcaster: map[string]record.EventBroadcaster{},
		eventNsRecorder:    map[string]record.EventRecorder{},
	}
}

type CreateEventHandler struct {
	log                logrus.FieldLogger
	clientSet          kubernetes.Interface
	recorderFactory    func(string, string) (record.EventBroadcaster, record.EventRecorder)
	mu                 sync.RWMutex
	eventNsBroadcaster map[string]record.EventBroadcaster
	eventNsRecorder    map[string]record.EventRecorder
}

func (h *CreateEventHandler) Handle(ctx context.Context, action *types.ClusterAction) error {
	req, ok := action.Data().(*types.ActionCreateEvent)
	if !ok {
		return fmt.Errorf("unexpected type %T for create event handler", action.Data())
	}
	namespace := req.ObjectRef.Namespace
	if namespace == "" {
		namespace = v1.NamespaceDefault
	}
	h.handleEventV1(ctx, req, namespace)
	return nil
}

func (h *CreateEventHandler) handleEventV1(_ context.Context, req *types.ActionCreateEvent, namespace string) {
	h.mu.RLock()
	h.log.Debug("handling create event action: %s type: %s", req.Action, req.EventType)
	if recorder, ok := h.eventNsRecorder[fmt.Sprintf("%s-%s", namespace, req.Reporter)]; ok {
		recorder.Event(&req.ObjectRef, v1.EventTypeNormal, req.Reason, req.Message)
		h.mu.RUnlock()
	} else {
		h.mu.RUnlock()
		h.mu.Lock()
		// Double check after acquiring the lock.
		if recorder, ok := h.eventNsRecorder[namespace]; !ok {
			broadcaster, rec := h.recorderFactory(namespace, req.Reporter)
			h.eventNsBroadcaster[fmt.Sprintf("%s-%s", namespace, req.Reporter)] = broadcaster
			h.eventNsRecorder[fmt.Sprintf("%s-%s", namespace, req.Reporter)] = rec
			rec.Event(&req.ObjectRef, req.EventType, req.Reason, req.Message)
		} else {
			recorder.Event(&req.ObjectRef, req.EventType, req.Reason, req.Message)
		}
		h.mu.Unlock()
	}
}
