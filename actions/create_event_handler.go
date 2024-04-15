package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"k8s.io/client-go/kubernetes"

	actiontypes "github.com/castai/cluster-controller/actions/types"
)

func newCreateEventHandler(log logrus.FieldLogger, clientset kubernetes.Interface) ActionHandler {
	return &createEventHandler{
		log:       log,
		clientset: clientset,
	}
}

type createEventHandler struct {
	log       logrus.FieldLogger
	clientset kubernetes.Interface
}

func (h *createEventHandler) Handle(ctx context.Context, action *actiontypes.ClusterAction) error {
	req, ok := action.Data().(*actiontypes.ActionCreateEvent)
	if !ok {
		return fmt.Errorf("unexpected type %T for create event handler", action.Data())
	}

	namespace := req.ObjectRef.Namespace
	if namespace == "" {
		namespace = v1.NamespaceDefault
	}

	event := &v1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%v.%x", req.ObjectRef.Name, req.EventTime.Unix()),
			Namespace: namespace,
		},
		EventTime:           metav1.NewMicroTime(req.EventTime),
		ReportingController: req.Reporter,
		ReportingInstance:   req.Reporter,
		InvolvedObject:      req.ObjectRef,
		Type:                req.EventType,
		Reason:              req.Reason,
		Action:              req.Action,
		Message:             req.Message,
		FirstTimestamp:      metav1.NewTime(req.EventTime),
		LastTimestamp:       metav1.NewTime(req.EventTime),
		Count:               1,
	}

	similarEvent, err := h.searchSimilarEvent(&req.ObjectRef, event)
	if err != nil {
		return fmt.Errorf("searching for similar event for ref %v: %w", req.ObjectRef, err)
	}

	if similarEvent != nil {
		event.Name = similarEvent.Name
		event.ResourceVersion = similarEvent.ResourceVersion
		event.FirstTimestamp = similarEvent.FirstTimestamp
		event.Count = similarEvent.Count + 1

		newData, err := json.Marshal(event)
		if err != nil {
			return fmt.Errorf("marshaling new event for ref %v: %w", req.ObjectRef, err)
		}

		oldData, err := json.Marshal(similarEvent)
		if err != nil {
			return fmt.Errorf("marshaling old event for ref %v: %w", req.ObjectRef, err)
		}

		patch, err := strategicpatch.CreateTwoWayMergePatch(oldData, newData, event)
		if err != nil {
			return fmt.Errorf("creating event merge patch for ref %v: %w", req.ObjectRef, err)
		}

		_, err = h.clientset.CoreV1().
			Events(event.Namespace).
			Patch(ctx, similarEvent.Name, types.StrategicMergePatchType, patch, metav1.PatchOptions{})
		if err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("patching event for ref %v: %w", req.ObjectRef, err)
		}
		// Patching might fail if the event was removed while we were constructing the request body, so just
		// recreate the event.
		if err == nil {
			return nil
		}
	}

	if _, err := h.clientset.CoreV1().Events(event.Namespace).Create(ctx, event, metav1.CreateOptions{}); err != nil {
		return fmt.Errorf("creating event for ref %v: %w", req.ObjectRef, err)
	}

	return nil
}

func (h *createEventHandler) searchSimilarEvent(ref *v1.ObjectReference, event *v1.Event) (*v1.Event, error) {
	// Scheme is not needed when searching for reference. Scheme is needed only for raw runtime objects.
	resp, err := h.clientset.CoreV1().Events(event.Namespace).Search(nil, ref)
	if err != nil {
		return nil, fmt.Errorf("searching events for ref %+v: %w", ref, err)
	}

	key := getEventKey(event)

	for i := range resp.Items {
		if getEventKey(&resp.Items[i]) != key {
			continue
		}
		return &resp.Items[i], nil
	}

	return nil, nil
}

func getEventKey(event *v1.Event) string {
	return strings.Join([]string{
		event.InvolvedObject.Kind,
		event.InvolvedObject.Namespace,
		event.InvolvedObject.Name,
		event.InvolvedObject.FieldPath,
		string(event.InvolvedObject.UID),
		event.InvolvedObject.APIVersion,
		event.Type,
		event.Reason,
		event.Action,
		event.Message,
		event.ReportingController,
	}, "")
}
