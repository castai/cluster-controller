package informer

import (
	listerv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
)

type nodeInformer struct {
	informer cache.SharedIndexInformer
	lister   listerv1.NodeLister
	indexers cache.Indexers
}

func (n *nodeInformer) Informer() cache.SharedIndexInformer {
	return n.informer
}

func (n *nodeInformer) Lister() listerv1.NodeLister {
	return n.lister
}

func (n *nodeInformer) HasSynced() bool {
	return n.informer.HasSynced()
}
