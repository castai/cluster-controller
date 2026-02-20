package informer

import (
	"context"
	"fmt"
	"sync"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	listerv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
)

type Predicate func(node *corev1.Node) (bool, error)

// NodeInformer provides domain operations for nodes.
// Lifecycle (start/stop/sync) is managed by Manager.
type NodeInformer interface {
	// Get retrieves a node by name from cache
	Get(name string) (*corev1.Node, error)

	// List returns all nodes from cache
	List() ([]*corev1.Node, error)

	// Wait watches for a node to meet a condition.
	// Returns a channel that signals when condition is met or context is canceled.
	// Used for event-driven waiting (e.g., wait for node to become ready).
	Wait(ctx context.Context, name string, condition Predicate) chan error
}

type observable struct {
	done      chan error
	condition Predicate
}

type nodeInformer struct {
	informer cache.SharedIndexInformer
	lister   listerv1.NodeLister
	indexers cache.Indexers
	logger   logrus.FieldLogger

	events       chan any
	registration cache.ResourceEventHandlerRegistration

	tracked map[string]observable
	mu      sync.Mutex
}

func NewNodeInformer(
	informer cache.SharedIndexInformer,
	lister listerv1.NodeLister,
) *nodeInformer {
	n := &nodeInformer{
		informer: informer,
		lister:   lister,
		tracked:  make(map[string]observable),
		events:   make(chan any),
	}
	return n
}

func (n *nodeInformer) Start(ctx context.Context) error {
	err := n.register()
	if err != nil {
		return err
	}
	go n.waitStop(ctx)
	return nil
}

func (n *nodeInformer) register() error {
	r, err := n.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			n.onEvent(obj)
		},
		UpdateFunc: func(oldObj, newObj any) {
			n.onEvent(newObj)
		},
	})
	if err != nil {
		return err
	}
	n.registration = r
	return nil
}

func (n *nodeInformer) waitStop(ctx context.Context) {
	<-ctx.Done()
	n.stop()
}

func (n *nodeInformer) stop() {
	n.mu.Lock()
	defer n.mu.Unlock()

	if err := n.informer.RemoveEventHandler(n.registration); err != nil {
		n.logger.WithError(err).Warn("failed to remove event handler")
	}
	n.registration = nil

	for name, observable := range n.tracked {
		delete(n.tracked, name)
		close(observable.done)
	}
}

func (n *nodeInformer) onEvent(object any) {
	node, ok := object.(*corev1.Node)
	if !ok {
		return
	}

	n.mu.Lock()
	defer n.mu.Unlock()

	observable, ok := n.tracked[node.Name]
	if !ok {
		return
	}

	ok, err := observable.condition(node)
	if ok || err != nil {
		select {
		case observable.done <- err:
		default:
		}
		delete(n.tracked, node.Name)
	}
}

func (n *nodeInformer) Get(name string) (*corev1.Node, error) {
	return n.lister.Get(name)
}

func (n *nodeInformer) List() ([]*corev1.Node, error) {
	nodes, err := n.lister.List(labels.Everything())
	if err != nil {
		return nil, err
	}
	return nodes, nil
}

func (n *nodeInformer) Wait(ctx context.Context, name string, condition Predicate) chan error {
	done := make(chan error, 1)

	n.mu.Lock()
	defer n.mu.Unlock()

	if _, exists := n.tracked[name]; exists {
		done <- fmt.Errorf("node %s is already being tracked", name)
		return done
	}

	node, err := n.lister.Get(name)
	if err == nil {
		ok, condErr := condition(node)
		if ok || condErr != nil {
			done <- condErr
			return done
		}
	}

	n.tracked[name] = observable{
		done:      done,
		condition: condition,
	}

	go func(name string) {
		<-ctx.Done()
		n.mu.Lock()
		defer n.mu.Unlock()
		o, exists := n.tracked[name]
		if !exists {
			return
		}
		delete(n.tracked, name)
		select {
		case o.done <- ctx.Err():
		default:
		}
	}(name)

	return done
}
