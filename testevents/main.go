package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/castai/cluster-controller/castai"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"os"
	"time"

	"github.com/castai/cluster-controller/actions"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
	"path/filepath"
)

//
//import (
//	"context"
//	"flag"
//	"fmt"
//	"math/rand"
//	"os"
//	"time"
//
//	corev1 "k8s.io/api/core/v1"
//	v1 "k8s.io/api/core/v1"
//	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
//	"k8s.io/client-go/kubernetes"
//	typedv1core "k8s.io/client-go/kubernetes/typed/core/v1"
//	"k8s.io/client-go/tools/clientcmd"
//	"k8s.io/client-go/tools/record"
//	"k8s.io/client-go/util/homedir"
//	"path/filepath"
//)
//
//var (
//	factory = func(clientset *kubernetes.Clientset, ns string) (record.EventBroadcaster, record.EventRecorder) {
//		eventBroadcaster := record.NewBroadcaster()
//		eventBroadcaster.StartRecordingToSink(&typedv1core.EventSinkImpl{Interface: clientset.CoreV1().Events(ns)})
//		eventBroadcaster.StartStructuredLogging(0)
//		return eventBroadcaster, eventBroadcaster.NewRecorder(nil, v1.EventSource{})
//	}
//	eventRecorder    record.EventRecorder
//	eventBroadcaster record.EventBroadcaster
//)
//
//func main() {
//	clientset, err := makeClientSet()
//	if err != nil {
//		fmt.Printf("Failed to create clientset: %v\n", err)
//		os.Exit(1)
//	}
//	eventBroadcaster, eventRecorder = factory(clientset, "default")
//	start := time.Now()
//	type testResult struct {
//		V1 time.Duration
//		V2 time.Duration
//	}
//	testResults := map[int]testResult{}
//	fmt.Printf("Creating %d events\n", 10)
//	for i := 0; i < 0; i++ {
//		V1(clientset)
//	}
//	V1result := time.Since(start)
//	start = time.Now()
//	for i := 0; i < 10; i++ {
//		V2()
//	}
//	V2Result := time.Since(start)
//	testResults[10] = testResult{
//		V1: V1result,
//		V2: V2Result,
//	}
//	fmt.Printf("---------------------------\n")
//	time.Sleep(10 * time.Second)
//	fmt.Printf("For %d events\n", 10)
//	fmt.Printf("V1 took %v\n", testResults[10].V1)
//	fmt.Printf("V2 took %v\n", testResults[10].V2)
//}

func makeClientSet() (*kubernetes.Clientset, error) {
	// Define the kubeconfig flag
	var kubeconfig *string
	if home := homedir.HomeDir(); home != "" {
		kubeconfig = flag.String("kubeconfig", filepath.Join(home, ".kube", "config"), "(optional) absolute path to the kubeconfig file")
	} else {
		kubeconfig = flag.String("kubeconfig", "", "absolute path to the kubeconfig file")
	}
	flag.Parse()

	// Build the config from the kubeconfig file path
	config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("Failed to build kubeconfig: %v\n", err)
	}
	// Create the clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("Failed to create clientset: %v\n", err)
	}
	return clientset, nil
}

//
//func V1(clientset *kubernetes.Clientset) {
//	// Create an event
//	namespace := "default"
//	event := &corev1.Event{
//		ObjectMeta: metav1.ObjectMeta{
//			GenerateName: "example-event-",
//			Namespace:    namespace,
//		},
//		InvolvedObject: corev1.ObjectReference{
//			Kind:      "Pod",
//			Namespace: namespace,
//			Name:      fmt.Sprintf("example-pod-%d", rand.Int()%100), // Pod name or any other involved object
//		},
//		Reason:  "ExampleReason",
//		Message: "This is a test event created by Go client",
//		Source: corev1.EventSource{
//			Component: "example-component",
//		},
//		FirstTimestamp: metav1.Time{Time: time.Now()},
//		LastTimestamp:  metav1.Time{Time: time.Now()},
//		Type:           "Normal", // Could also be "Warning"
//	}
//
//	// Use the Events client to create the event
//	eventsClient := clientset.CoreV1().Events(namespace)
//	_, err := eventsClient.Create(context.TODO(), event, metav1.CreateOptions{})
//	if err != nil {
//		fmt.Printf("Failed to create event: %v\n", err)
//		os.Exit(1)
//	}
//	// fmt.Printf("Event created: %v\n", result.Name)
//}
//
//func V2() {
//	// Create an event
//	namespace := "default"
//	objectRef := &corev1.ObjectReference{
//		Kind:      "Pod",
//		Namespace: namespace,
//		Name:      fmt.Sprintf("example-pod-%d", rand.Int()), // Pod name or any other involved object
//	}
//
//	eventRecorder.Event(objectRef, v1.EventTypeNormal,
//		"ExampleReasonV2", fmt.Sprintf("%s %s", "aaa", "bbb"))
//}

func main() {
	clientset, err := makeClientSet()
	if err != nil {
		fmt.Printf("Failed to create clientset: %v\n", err)
		os.Exit(1)
	}
	reporters := []string{"autoscaler.cast.ai", "castai.cluster-controller"}
	handler := actions.NewCreateEventHandler(logrus.New(), clientset)
	for i := 0; i < 4; i++ {
		err = handler.Handle(
			context.Background(),
			&castai.ClusterAction{
				ID: "123",
				ActionCreateEvent: &castai.ActionCreateEvent{
					Reporter: reporters[i%2],
					ObjectRef: corev1.ObjectReference{
						Kind:      "Pod",
						Namespace: "default",
						Name:      fmt.Sprintf("example-pod-%s", uuid.New().String()[:4]),
					},
					EventType: "Normal",
					Reason:    "Upscaling",
					Action:    "create_event",
					Message:   "Impossible to schedule pod, no instance type matches selectors. For example: kubernetes.io/os=windows.",
				},
			},
		)
		if err != nil {
			fmt.Printf("Failed to handle event: %v\n", err)
			os.Exit(1)
		}
	}
	time.Sleep(10 * time.Second)
}
