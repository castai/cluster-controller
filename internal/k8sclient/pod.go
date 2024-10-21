package k8sclient

import (
	"context"
	"fmt"
	v1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/api/policy/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (k *Client) DeletePod(ctx context.Context, namespace, name string, options metav1.DeleteOptions) error {
	return k.CoreV1().Pods(namespace).Delete(ctx, name, options)
}

func (k *Client) EvictPod(ctx context.Context, namespace, podName string) error {
	groupVersion, err := k.CheckEvictionSupport()
	if err != nil {
		return err
	}
	if groupVersion == nil {
		return fmt.Errorf("eviction group version is nil")
	}

	if *groupVersion == policyv1.SchemeGroupVersion {
		err = k.PolicyV1().Evictions(namespace).Evict(ctx, &policyv1.Eviction{
			ObjectMeta: metav1.ObjectMeta{
				Name:      podName,
				Namespace: namespace,
			},
		})
	} else {
		err = k.CoreV1().Pods(namespace).EvictV1beta1(ctx, &v1beta1.Eviction{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "policy/v1beta1",
				Kind:       "Eviction",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      podName,
				Namespace: namespace,
			},
		})
	}

	return err
}

func (k *Client) ListPods(ctx context.Context, namespace string, opts metav1.ListOptions) (*v1.PodList, error) {
	return k.CoreV1().Pods(namespace).List(ctx, opts)
}
