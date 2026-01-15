package informer

import (
	listerstoragev1 "k8s.io/client-go/listers/storage/v1"
	"k8s.io/client-go/tools/cache"
)

// VANodeNameIndexer is the indexer key for looking up VolumeAttachments by node name.
const VANodeNameIndexer = "spec.nodeName"

type vaInformer struct {
	informer cache.SharedIndexInformer
	lister   listerstoragev1.VolumeAttachmentLister
	indexers cache.Indexers
}

func (v *vaInformer) Informer() cache.SharedIndexInformer {
	return v.informer
}

func (v *vaInformer) Lister() listerstoragev1.VolumeAttachmentLister {
	return v.lister
}

func (v *vaInformer) HasSynced() bool {
	return v.informer.HasSynced()
}
