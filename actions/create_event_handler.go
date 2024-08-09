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

	"github.com/castai/cluster-controller/castai"
)

func newCreateEventHandler(log logrus.FieldLogger, clientset kubernetes.Interface) ActionHandler {
	factory := func(ns string) (record.EventBroadcaster, record.EventRecorder) {
		eventBroadcaster := record.NewBroadcaster()
		eventBroadcaster.StartRecordingToSink(&typedv1core.EventSinkImpl{Interface: clientset.CoreV1().Events(ns)})
		eventBroadcaster.StartStructuredLogging(0)
		log.Debug("create new broadcaster and recorder for namespace: %s", ns)
		// Create an event recorder
		return eventBroadcaster, eventBroadcaster.NewRecorder(nil, v1.EventSource{})
	}
	return &createEventHandler{
		log:                log,
		clientSet:          clientset,
		recorderFactory:    factory,
		eventNsBroadcaster: map[string]record.EventBroadcaster{},
		eventNsRecorder:    map[string]record.EventRecorder{},
	}
}

type createEventHandler struct {
	log                logrus.FieldLogger
	clientSet          kubernetes.Interface
	recorderFactory    func(string) (record.EventBroadcaster, record.EventRecorder)
	mu                 sync.RWMutex
	eventNsBroadcaster map[string]record.EventBroadcaster
	eventNsRecorder    map[string]record.EventRecorder
}

func (h *createEventHandler) Handle(ctx context.Context, action *castai.ClusterAction) error {
	h.log.Infof("handling create_event action: %s", action.ID)
	req, ok := action.Data().(*castai.ActionCreateEvent)
	if !ok {
		return fmt.Errorf("unexpected type %T for create event handler", action.Data())
	}
	namespace := req.ObjectRef.Namespace
	if namespace == "" {
		namespace = v1.NamespaceDefault
	}
	h.log.Infof("handling create_event action: %s type: %s namespace: %s", req.Action, req.EventType, namespace)
	h.handleEventV1(ctx, req, namespace)
	return nil
}

func (h *createEventHandler) handleEventV1(_ context.Context, req *castai.ActionCreateEvent, namespace string) {
	h.mu.RLock()
	h.log.Debug("handling create_event action: %s type: %s", req.Action, req.EventType)
	if recorder, ok := h.eventNsRecorder[namespace]; ok {
		recorder.Eventf(&req.ObjectRef, v1.EventTypeNormal, req.Reason, req.Action, req.Message)
		h.mu.RUnlock()
	} else {
		h.mu.RUnlock()
		h.mu.Lock()
		// Double check after acquiring the lock.
		if recorder, ok := h.eventNsRecorder[namespace]; !ok {
			broadcaster, rec := h.recorderFactory(namespace)
			h.eventNsBroadcaster[namespace] = broadcaster
			h.eventNsRecorder[namespace] = rec
			rec.Eventf(&req.ObjectRef, v1.EventTypeNormal, req.Reason, req.Action, req.Message)
		} else {
			recorder.Eventf(&req.ObjectRef, v1.EventTypeNormal, req.Reason, req.Action, req.Message)
		}
		h.mu.Unlock()
	}
}
