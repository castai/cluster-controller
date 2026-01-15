package informer

import (
	"context"
	"fmt"
	"sync"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	listerv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
)

type Predicate func(node *corev1.Node) (bool, error)

type NodeInformer interface {
	Start(ctx context.Context) error
	Wait(ctx context.Context, name string, condition Predicate) chan error
	Informer() cache.SharedIndexInformer
	Lister() listerv1.NodeLister
	Indexers() cache.Indexers
	SetIndexers(indexers cache.Indexers)
	HasSynced() bool
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
) NodeInformer {
	n := &nodeInformer{
		informer: informer,
		lister:   lister,
		tracked:  make(map[string]observable),
		events:   make(chan any),
	}
	return n
}

func (n *nodeInformer) Start(ctx context.Context) error {
	err := n.register(n.events)
	if err != nil {
		return err
	}
	go n.run(ctx)
	return nil
}

func (n *nodeInformer) register(events chan any) error {
	r, err := n.informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj any) {
			events <- obj
		},
		UpdateFunc: func(oldObj, newObj any) {
			events <- newObj
		},
	})
	if err != nil {
		return err
	}
	n.registration = r
	return nil
}

func (n *nodeInformer) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			n.stop()
			return
		case object, ok := <-n.events:
			if !ok {
				return
			}
			n.onEvent(ctx, object)
		}
	}
}

func (n *nodeInformer) stop() {
	n.mu.Lock()
	defer n.mu.Unlock()

	if err := n.informer.RemoveEventHandler(n.registration); err != nil {
		n.logger.WithError(err).Warn("failed to remove event handler")
	}
	n.registration = nil

	for name := range n.tracked {
		delete(n.tracked, name)
	}
}

func (n *nodeInformer) onEvent(_ context.Context, object any) {
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
		_, exists := n.tracked[name]
		if !exists {
			return
		}
		delete(n.tracked, name)
	}(name)

	return done
}

func (n *nodeInformer) Informer() cache.SharedIndexInformer {
	return n.informer
}

func (n *nodeInformer) Lister() listerv1.NodeLister {
	return n.lister
}

func (n *nodeInformer) Indexers() cache.Indexers {
	return n.indexers
}

func (n *nodeInformer) SetIndexers(indexers cache.Indexers) {
	n.indexers = indexers
}

func (n *nodeInformer) HasSynced() bool {
	return n.informer.HasSynced()
}
