package informer

import (
	listerv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
)

type podInformer struct {
	informer cache.SharedIndexInformer
	lister   listerv1.PodLister
	indexers cache.Indexers
}

func (p *podInformer) Informer() cache.SharedIndexInformer {
	return p.informer
}

func (p *podInformer) Lister() listerv1.PodLister {
	return p.lister
}

func (p *podInformer) HasSynced() bool {
	return p.informer.HasSynced()
}
