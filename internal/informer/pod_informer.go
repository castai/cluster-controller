package informer

import (
	"errors"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	listerv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
)

var ErrIndexerMissing = errors.New("missing indexer")

const PodIndexerName = "spec.NodeName"

// PodInformer provides domain operations for pods.
type PodInformer interface {
	// Get retrieves a pod by namespace and name from cache
	Get(namespace, name string) (*corev1.Pod, error)

	// List returns all pods from cache
	List() ([]*corev1.Pod, error)

	// ListByNode returns all pods running on a specific node
	ListByNode(nodeName string) ([]*corev1.Pod, error)

	// ListByNamespace returns all pods in a namespace
	ListByNamespace(namespace string) ([]*corev1.Pod, error)
}

type podInformer struct {
	informer cache.SharedIndexInformer
	lister   listerv1.PodLister
	indexers cache.Indexers
}

func NewPodInformer(
	informer cache.SharedIndexInformer,
	lister listerv1.PodLister,
	indexer cache.Indexer,
) *podInformer {
	p := &podInformer{
		informer: informer,
		lister:   lister,
	}
	return p
}

// Get retrieves a pod by namespace and name from cache
func (p *podInformer) Get(namespace, name string) (*corev1.Pod, error) {
	return p.lister.Pods(namespace).Get(name)
}

// List returns all pods from cache
func (p *podInformer) List() ([]*corev1.Pod, error) {
	return p.lister.List(labels.Everything())
}

// ListByNode returns all pods running on a specific node
func (p *podInformer) ListByNode(nodeName string) ([]*corev1.Pod, error) {
	objects, err := p.informer.GetIndexer().ByIndex(PodIndexerName, nodeName)
	if err != nil {
		return nil, err
	}

	pods := make([]*corev1.Pod, 0, len(objects))
	for _, obj := range objects {
		pod, ok := obj.(*corev1.Pod)
		if ok {
			pods = append(pods, pod)
		}
	}
	return pods, nil
}

// ListByNamespace returns all pods in a namespace
func (p *podInformer) ListByNamespace(namespace string) ([]*corev1.Pod, error) {
	return p.lister.Pods(namespace).List(labels.Everything())
}
