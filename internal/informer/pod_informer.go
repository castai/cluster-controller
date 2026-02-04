package informer

import (
	"errors"

	listerv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
)

var ErrIndexerMissing = errors.New("missing indexer")

const PodIndexerName = "spec.NodeName"

type PodInformer struct {
	informer cache.SharedIndexInformer
	lister   listerv1.PodLister
	indexers cache.Indexers
}

func NewPodInformer(
	informer cache.SharedIndexInformer,
	lister listerv1.PodLister,
	indexer cache.Indexer,
) *PodInformer {
	p := &PodInformer{
		informer: informer,
		lister:   lister,
	}
	return p
}

func (p *PodInformer) Informer() cache.SharedIndexInformer {
	return p.informer
}

func (p *PodInformer) Lister() listerv1.PodLister {
	return p.lister
}

func (p *PodInformer) HasSynced() bool {
	return p.informer.HasSynced()
}
