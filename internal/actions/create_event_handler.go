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

	"github.com/castai/cluster-controller/internal/castai"
)

var _ ActionHandler = &CreateEventHandler{}

func NewCreateEventHandler(log logrus.FieldLogger, clientset kubernetes.Interface) *CreateEventHandler {
	factory := func(ns, reporter string) (record.EventBroadcaster, record.EventRecorder) {
		eventBroadcaster := record.NewBroadcaster()
		eventBroadcaster.StartRecordingToSink(&typedv1core.EventSinkImpl{Interface: clientset.CoreV1().Events(ns)})
		eventBroadcaster.StartStructuredLogging(0)
		log.Debug("create new broadcaster and recorder for namespace: %s", ns)
		// Create an event recorder.
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

func (h *CreateEventHandler) Handle(ctx context.Context, action *castai.ClusterAction) error {
	req, ok := action.Data().(*castai.ActionCreateEvent)
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

func (h *CreateEventHandler) handleEventV1(_ context.Context, req *castai.ActionCreateEvent, namespace string) {
	h.log.Debugf("handling create event action: %s type: %s", req.Action, req.EventType)
	if recorder, ok := h.getRecorder(namespace, req.Reporter); ok {
		recorder.Event(&req.ObjectRef, v1.EventTypeNormal, req.Reason, req.Message)
	} else {
		rec := h.createRecorder(namespace, req.Reporter)
		rec.Event(&req.ObjectRef, req.EventType, req.Reason, req.Message)
	}
}

func (h *CreateEventHandler) getRecorder(namespace, reporter string) (record.EventRecorder, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	recorder, ok := h.eventNsRecorder[fmt.Sprintf("%s-%s", namespace, reporter)]
	return recorder, ok
}

func (h *CreateEventHandler) createRecorder(namespace, reporter string) record.EventRecorder {
	h.mu.Lock()
	defer h.mu.Unlock()

	key := fmt.Sprintf("%s-%s", namespace, reporter)
	if _, ok := h.eventNsRecorder[key]; !ok {
		h.log.Infof("creating event recorder and broadcaster for %v", fmt.Sprintf("%s-%s", namespace, reporter))
		broadcaster, rec := h.recorderFactory(namespace, reporter)
		h.eventNsBroadcaster[key] = broadcaster
		h.eventNsRecorder[key] = rec
	}

	return h.eventNsRecorder[key]
}

func (h *CreateEventHandler) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	for _, broadcaster := range h.eventNsBroadcaster {
		broadcaster.Shutdown()
	}
	h.eventNsBroadcaster = map[string]record.EventBroadcaster{}
	h.eventNsRecorder = map[string]record.EventRecorder{}

	return nil
}
