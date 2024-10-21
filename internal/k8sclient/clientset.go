//go:generate mockgen -package=mock_actions -destination ./mock/kubernetes.go k8s.io/client-go/kubernetes Interface
package k8sclient

import (
	"context"
	"fmt"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes"
	"k8s.io/kubectl/pkg/drain"
	"sync"
	"sync/atomic"
	"time"
)

type ClientSet interface {
	CheckEvictionSupport() (schema.GroupVersion, error)
	GetNode(name string) (*v1.Node, error)
	DeleteNode(ctx context.Context, name string) error
	PatchNode(ctx context.Context, name string, data []byte) error
	PatchNodeStatus(ctx context.Context, name string, patch []byte) error

	DeletePod(ctx context.Context, namespace, name string, options metav1.DeleteOptions) error
	EvictPod(ctx context.Context, namespace, name string) error
	ListPods(ctx context.Context, namespace string, opts metav1.ListOptions) (*v1.PodList, error)

	StorageVolumeAttachmentList(ctx context.Context) (*storagev1.VolumeAttachmentList, error)
}

type Client struct {
	kubernetes.Interface
	log             logrus.FieldLogger
	evictionSupport atomic.Value

	nodes map[string]*v1.Node
	mu    sync.RWMutex
}

type evictionSupport struct {
	groupVersion *schema.GroupVersion
	timestamp    int64
}

func NewClient(log logrus.FieldLogger, client kubernetes.Interface) *Client {
	return &Client{
		Interface: client,
		log:       log,
		nodes:     make(map[string]*v1.Node),
	}
}

func (k *Client) CheckEvictionSupport() (*schema.GroupVersion, error) {
	support := k.evictionSupport.Load()
	if support != nil {
		s := support.(evictionSupport)
		if time.Since(time.Unix(s.timestamp, 0)) < time.Hour {
			return s.groupVersion, nil
		}
	}
	groupVersion, err := drain.CheckEvictionSupport(k)
	if err != nil {
		return nil, fmt.Errorf("checking eviction support: %w", err)
	}
	k.evictionSupport.Store(evictionSupport{
		groupVersion: &groupVersion,
		timestamp:    time.Now().Unix(),
	})

	return &groupVersion, nil
}

func (k *Client) StorageVolumeAttachmentList(ctx context.Context) (*storagev1.VolumeAttachmentList, error) {
	// ?? why without filter by node???
	return k.StorageV1().VolumeAttachments().List(ctx, metav1.ListOptions{})
}
