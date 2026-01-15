package volume

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"

	"github.com/castai/cluster-controller/internal/informer"
)

func newTestWaiterVAInformer(t *testing.T, vas []*storagev1.VolumeAttachment, additionalObjs ...runtime.Object) (cache.Indexer, kubernetes.Interface) {
	t.Helper()

	objs := make([]runtime.Object, 0, len(vas)+len(additionalObjs))
	for _, va := range vas {
		objs = append(objs, va)
	}
	objs = append(objs, additionalObjs...)

	clientset := fake.NewClientset(objs...)
	factory := informers.NewSharedInformerFactory(clientset, 0)
	vaInformer := factory.Storage().V1().VolumeAttachments()

	err := vaInformer.Informer().AddIndexers(cache.Indexers{
		informer.VANodeNameIndexer: func(obj any) ([]string, error) {
			va, ok := obj.(*storagev1.VolumeAttachment)
			if !ok {
				return nil, nil
			}
			return []string{va.Spec.NodeName}, nil
		},
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	factory.Start(ctx.Done())
	synced := factory.WaitForCacheSync(ctx.Done())
	for typ, ok := range synced {
		require.True(t, ok, "failed to sync informer for %v", typ)
	}

	return vaInformer.Informer().GetIndexer(), clientset
}

func vaStrPtr(s string) *string {
	return &s
}

func TestDetachmentWaiter_Wait(t *testing.T) {
	t.Parallel()

	t.Run("should return immediately when no VAs on node", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := require.New(t)
			log := logrus.New()

			vaIndexer, clientset := newTestWaiterVAInformer(t, nil)
			waiter := NewDetachmentWaiter(clientset, vaIndexer, 50*time.Millisecond, 0)

			err := waiter.Wait(context.Background(), log, DetachmentWaitOptions{
				NodeName: "node1",
				Timeout:  1 * time.Second,
			})
			r.NoError(err)
		})
	})

	t.Run("should complete when VAs are deleted", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := require.New(t)
			log := logrus.New()

			va := &storagev1.VolumeAttachment{
				ObjectMeta: metav1.ObjectMeta{Name: "va1"},
				Spec: storagev1.VolumeAttachmentSpec{
					NodeName: "node1",
					Source:   storagev1.VolumeAttachmentSource{PersistentVolumeName: vaStrPtr("pv1")},
				},
			}
			vaIndexer, clientset := newTestWaiterVAInformer(t, []*storagev1.VolumeAttachment{va})
			waiter := NewDetachmentWaiter(clientset, vaIndexer, 50*time.Millisecond, 0)

			go func() {
				time.Sleep(100 * time.Millisecond)
				_ = clientset.StorageV1().VolumeAttachments().Delete(context.Background(), va.Name, metav1.DeleteOptions{})
			}()

			err := waiter.Wait(context.Background(), log, DetachmentWaitOptions{
				NodeName: "node1",
				Timeout:  2 * time.Second,
			})
			r.NoError(err)
		})
	})

	t.Run("should timeout gracefully and return nil", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := require.New(t)
			log := logrus.New()

			va := &storagev1.VolumeAttachment{
				ObjectMeta: metav1.ObjectMeta{Name: "va1"},
				Spec: storagev1.VolumeAttachmentSpec{
					NodeName: "node1",
					Source:   storagev1.VolumeAttachmentSource{PersistentVolumeName: vaStrPtr("pv1")},
				},
			}
			vaIndexer, clientset := newTestWaiterVAInformer(t, []*storagev1.VolumeAttachment{va})
			waiter := NewDetachmentWaiter(clientset, vaIndexer, 50*time.Millisecond, 0)

			err := waiter.Wait(context.Background(), log, DetachmentWaitOptions{
				NodeName: "node1",
				Timeout:  150 * time.Millisecond,
			})

			var vaErr *DetachmentError
			r.ErrorAs(err, &vaErr)
			r.Equal([]string{"va1"}, vaErr.RemainingVAs)
		})
	})

	t.Run("should respect context cancellation", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := require.New(t)
			log := logrus.New()

			va := &storagev1.VolumeAttachment{
				ObjectMeta: metav1.ObjectMeta{Name: "va1"},
				Spec: storagev1.VolumeAttachmentSpec{
					NodeName: "node1",
					Source:   storagev1.VolumeAttachmentSource{PersistentVolumeName: vaStrPtr("pv1")},
				},
			}
			vaIndexer, clientset := newTestWaiterVAInformer(t, []*storagev1.VolumeAttachment{va})
			waiter := NewDetachmentWaiter(clientset, vaIndexer, 50*time.Millisecond, 0)

			ctx, cancel := context.WithCancel(context.Background())

			go func() {
				time.Sleep(50 * time.Millisecond)
				cancel()
			}()

			err := waiter.Wait(ctx, log, DetachmentWaitOptions{
				NodeName: "node1",
				Timeout:  5 * time.Second,
			})
			r.ErrorIs(err, context.Canceled)
		})
	})

	t.Run("should only wait for VAs on specified node", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := require.New(t)
			log := logrus.New()

			vaNode1 := &storagev1.VolumeAttachment{
				ObjectMeta: metav1.ObjectMeta{Name: "va-node1"},
				Spec: storagev1.VolumeAttachmentSpec{
					NodeName: "node1",
					Source:   storagev1.VolumeAttachmentSource{PersistentVolumeName: vaStrPtr("pv1")},
				},
			}
			vaNode2 := &storagev1.VolumeAttachment{
				ObjectMeta: metav1.ObjectMeta{Name: "va-node2"},
				Spec: storagev1.VolumeAttachmentSpec{
					NodeName: "node2",
					Source:   storagev1.VolumeAttachmentSource{PersistentVolumeName: vaStrPtr("pv2")},
				},
			}

			vaIndexer, clientset := newTestWaiterVAInformer(t, []*storagev1.VolumeAttachment{vaNode1, vaNode2})
			waiter := NewDetachmentWaiter(clientset, vaIndexer, 50*time.Millisecond, 0)

			go func() {
				time.Sleep(100 * time.Millisecond)
				_ = clientset.StorageV1().VolumeAttachments().Delete(context.Background(), vaNode1.Name, metav1.DeleteOptions{})
			}()

			err := waiter.Wait(context.Background(), log, DetachmentWaitOptions{
				NodeName: "node1",
				Timeout:  2 * time.Second,
			})
			r.NoError(err)
		})
	})

	t.Run("should exclude VAs from excluded pods", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := require.New(t)
			log := logrus.New()

			pvc := &v1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{Name: "pvc-ds", Namespace: "default"},
				Spec:       v1.PersistentVolumeClaimSpec{VolumeName: "pv-ds"},
			}
			vaFromDS := &storagev1.VolumeAttachment{
				ObjectMeta: metav1.ObjectMeta{Name: "va-ds"},
				Spec: storagev1.VolumeAttachmentSpec{
					NodeName: "node1",
					Source:   storagev1.VolumeAttachmentSource{PersistentVolumeName: vaStrPtr("pv-ds")},
				},
			}
			vaFromRegular := &storagev1.VolumeAttachment{
				ObjectMeta: metav1.ObjectMeta{Name: "va-regular"},
				Spec: storagev1.VolumeAttachmentSpec{
					NodeName: "node1",
					Source:   storagev1.VolumeAttachmentSource{PersistentVolumeName: vaStrPtr("pv-regular")},
				},
			}

			controller := true
			dsPod := v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ds-pod",
					Namespace: "default",
					OwnerReferences: []metav1.OwnerReference{
						{Kind: "DaemonSet", Controller: &controller},
					},
				},
				Spec: v1.PodSpec{
					NodeName: "node1",
					Volumes: []v1.Volume{
						{
							Name: "data",
							VolumeSource: v1.VolumeSource{
								PersistentVolumeClaim: &v1.PersistentVolumeClaimVolumeSource{ClaimName: "pvc-ds"},
							},
						},
					},
				},
			}

			vaIndexer, clientset := newTestWaiterVAInformer(t, []*storagev1.VolumeAttachment{vaFromDS, vaFromRegular}, pvc)
			waiter := NewDetachmentWaiter(clientset, vaIndexer, 50*time.Millisecond, 0)

			go func() {
				time.Sleep(100 * time.Millisecond)
				_ = clientset.StorageV1().VolumeAttachments().Delete(context.Background(), "va-regular", metav1.DeleteOptions{})
			}()

			err := waiter.Wait(context.Background(), log, DetachmentWaitOptions{
				NodeName:      "node1",
				Timeout:       2 * time.Second,
				PodsToExclude: []v1.Pod{dsPod},
			})
			r.NoError(err)
		})
	})

	t.Run("should exclude VAs from static pods", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := require.New(t)
			log := logrus.New()

			pvc := &v1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{Name: "pvc-static", Namespace: "default"},
				Spec:       v1.PersistentVolumeClaimSpec{VolumeName: "pv-static"},
			}
			vaFromStatic := &storagev1.VolumeAttachment{
				ObjectMeta: metav1.ObjectMeta{Name: "va-static"},
				Spec: storagev1.VolumeAttachmentSpec{
					NodeName: "node1",
					Source:   storagev1.VolumeAttachmentSource{PersistentVolumeName: vaStrPtr("pv-static")},
				},
			}
			vaFromRegular := &storagev1.VolumeAttachment{
				ObjectMeta: metav1.ObjectMeta{Name: "va-regular"},
				Spec: storagev1.VolumeAttachmentSpec{
					NodeName: "node1",
					Source:   storagev1.VolumeAttachmentSource{PersistentVolumeName: vaStrPtr("pv-regular")},
				},
			}

			controller := true
			staticPod := v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "static-pod",
					Namespace: "default",
					OwnerReferences: []metav1.OwnerReference{
						{Kind: "Node", Controller: &controller},
					},
				},
				Spec: v1.PodSpec{
					NodeName: "node1",
					Volumes: []v1.Volume{
						{
							Name: "data",
							VolumeSource: v1.VolumeSource{
								PersistentVolumeClaim: &v1.PersistentVolumeClaimVolumeSource{ClaimName: "pvc-static"},
							},
						},
					},
				},
			}

			vaIndexer, clientset := newTestWaiterVAInformer(t, []*storagev1.VolumeAttachment{vaFromStatic, vaFromRegular}, pvc)
			waiter := NewDetachmentWaiter(clientset, vaIndexer, 50*time.Millisecond, 0)

			go func() {
				time.Sleep(100 * time.Millisecond)
				_ = clientset.StorageV1().VolumeAttachments().Delete(context.Background(), "va-regular", metav1.DeleteOptions{})
			}()

			err := waiter.Wait(context.Background(), log, DetachmentWaitOptions{
				NodeName:      "node1",
				Timeout:       2 * time.Second,
				PodsToExclude: []v1.Pod{staticPod},
			})
			r.NoError(err)
		})
	})
}
