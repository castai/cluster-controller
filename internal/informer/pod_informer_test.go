package informer

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

func TestPodInformer_Informer(t *testing.T) {
	t.Parallel()

	log := logrus.New()
	clientset := fake.NewClientset()
	manager := NewManager(log, clientset, time.Hour, EnablePodInformer())

	informer := manager.pods.informer
	lister := manager.pods.lister

	require.NotNil(t, informer)
	require.NotNil(t, lister)
	require.False(t, manager.pods.informer.HasSynced())
}

func TestPodInformer_Get(t *testing.T) {
	t.Parallel()

	pod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod-1",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			NodeName: "node-1",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}

	pod2 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod-2",
			Namespace: "kube-system",
		},
		Spec: corev1.PodSpec{
			NodeName: "node-2",
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}

	tests := []struct {
		name        string
		initialPods []*corev1.Pod
		namespace   string
		podName     string
		wantPod     *corev1.Pod
		wantErr     bool
	}{
		{
			name:        "get existing pod in default namespace",
			initialPods: []*corev1.Pod{pod1, pod2},
			namespace:   "default",
			podName:     "test-pod-1",
			wantPod:     pod1,
			wantErr:     false,
		},
		{
			name:        "get existing pod in kube-system namespace",
			initialPods: []*corev1.Pod{pod1, pod2},
			namespace:   "kube-system",
			podName:     "test-pod-2",
			wantPod:     pod2,
			wantErr:     false,
		},
		{
			name:        "get non-existent pod",
			initialPods: []*corev1.Pod{pod1},
			namespace:   "default",
			podName:     "non-existent-pod",
			wantPod:     nil,
			wantErr:     true,
		},
		{
			name:        "get pod from wrong namespace",
			initialPods: []*corev1.Pod{pod1},
			namespace:   "kube-system",
			podName:     "test-pod-1",
			wantPod:     nil,
			wantErr:     true,
		},
		{
			name:        "get from empty cache",
			initialPods: nil,
			namespace:   "default",
			podName:     "test-pod-1",
			wantPod:     nil,
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				var initialObjects []runtime.Object
				for _, pod := range tt.initialPods {
					initialObjects = append(initialObjects, pod)
				}

				clientSet := fake.NewClientset(initialObjects...)
				log := logrus.New()

				infMgr := NewManager(log, clientSet, 10*time.Minute, EnablePodInformer())

				ctx, cancel := context.WithCancel(t.Context())
				t.Cleanup(func() {
					cancel()
				})

				go func() {
					_ = infMgr.Start(ctx)
				}()
				synctest.Wait()

				pod, err := infMgr.Pods().Get(tt.namespace, tt.podName)

				if tt.wantErr {
					require.Error(t, err)
					require.Nil(t, pod)
				} else {
					require.NoError(t, err)
					require.NotNil(t, pod)
					require.Equal(t, tt.wantPod.Name, pod.Name)
					require.Equal(t, tt.wantPod.Namespace, pod.Namespace)
				}
			})
		})
	}
}

func TestPodInformer_List(t *testing.T) {
	t.Parallel()

	pod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod-1",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			NodeName: "node-1",
		},
	}

	pod2 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod-2",
			Namespace: "kube-system",
		},
		Spec: corev1.PodSpec{
			NodeName: "node-2",
		},
	}

	pod3 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod-3",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			NodeName: "node-1",
		},
	}

	tests := []struct {
		name        string
		initialPods []*corev1.Pod
		wantCount   int
		wantErr     bool
	}{
		{
			name:        "list multiple pods",
			initialPods: []*corev1.Pod{pod1, pod2, pod3},
			wantCount:   3,
			wantErr:     false,
		},
		{
			name:        "list single pod",
			initialPods: []*corev1.Pod{pod1},
			wantCount:   1,
			wantErr:     false,
		},
		{
			name:        "list empty cache",
			initialPods: nil,
			wantCount:   0,
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				var initialObjects []runtime.Object
				for _, pod := range tt.initialPods {
					initialObjects = append(initialObjects, pod)
				}

				clientSet := fake.NewClientset(initialObjects...)
				log := logrus.New()

				infMgr := NewManager(log, clientSet, 10*time.Minute, EnablePodInformer())

				ctx, cancel := context.WithCancel(t.Context())
				t.Cleanup(func() {
					cancel()
				})

				go func() {
					_ = infMgr.Start(ctx)
				}()
				synctest.Wait()

				pods, err := infMgr.Pods().List()

				if tt.wantErr {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
					require.Len(t, pods, tt.wantCount)

					// Verify that the returned pods match the initial pods
					if tt.wantCount > 0 {
						podKeys := make(map[string]bool)
						for _, pod := range pods {
							key := pod.Namespace + "/" + pod.Name
							podKeys[key] = true
						}

						for _, expectedPod := range tt.initialPods {
							expectedKey := expectedPod.Namespace + "/" + expectedPod.Name
							require.True(t, podKeys[expectedKey], "expected pod %s not found in list", expectedKey)
						}
					}
				}
			})
		})
	}
}

func TestPodInformer_ListByNode(t *testing.T) {
	t.Parallel()

	pod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod-1",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			NodeName: "node-1",
		},
	}

	pod2 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod-2",
			Namespace: "kube-system",
		},
		Spec: corev1.PodSpec{
			NodeName: "node-1",
		},
	}

	pod3 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod-3",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			NodeName: "node-2",
		},
	}

	pod4 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod-4",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			NodeName: "", // unscheduled pod
		},
	}

	tests := []struct {
		name        string
		initialPods []*corev1.Pod
		nodeName    string
		wantCount   int
		wantPods    []*corev1.Pod
		wantErr     bool
	}{
		{
			name:        "list pods on node-1",
			initialPods: []*corev1.Pod{pod1, pod2, pod3},
			nodeName:    "node-1",
			wantCount:   2,
			wantPods:    []*corev1.Pod{pod1, pod2},
			wantErr:     false,
		},
		{
			name:        "list pods on node-2",
			initialPods: []*corev1.Pod{pod1, pod2, pod3},
			nodeName:    "node-2",
			wantCount:   1,
			wantPods:    []*corev1.Pod{pod3},
			wantErr:     false,
		},
		{
			name:        "list pods on node with no pods",
			initialPods: []*corev1.Pod{pod1, pod2},
			nodeName:    "node-3",
			wantCount:   0,
			wantPods:    nil,
			wantErr:     false,
		},
		{
			name:        "list pods with empty cache",
			initialPods: nil,
			nodeName:    "node-1",
			wantCount:   0,
			wantPods:    nil,
			wantErr:     false,
		},
		{
			name:        "list pods excludes unscheduled pods",
			initialPods: []*corev1.Pod{pod1, pod4},
			nodeName:    "node-1",
			wantCount:   1,
			wantPods:    []*corev1.Pod{pod1},
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				var initialObjects []runtime.Object
				for _, pod := range tt.initialPods {
					initialObjects = append(initialObjects, pod)
				}

				clientSet := fake.NewClientset(initialObjects...)
				log := logrus.New()

				infMgr := NewManager(log, clientSet, 10*time.Minute, EnablePodInformer(), WithDefaultPodNodeNameIndexer())

				ctx, cancel := context.WithCancel(t.Context())
				t.Cleanup(func() {
					cancel()
				})

				go func() {
					_ = infMgr.Start(ctx)
				}()
				synctest.Wait()

				pods, err := infMgr.Pods().ListByNode(tt.nodeName)

				if tt.wantErr {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
					require.Len(t, pods, tt.wantCount)

					// Verify that all returned pods are on the specified node
					for _, pod := range pods {
						require.Equal(t, tt.nodeName, pod.Spec.NodeName)
					}

					// Verify that the returned pods match the expected pods
					if tt.wantCount > 0 {
						podKeys := make(map[string]bool)
						for _, pod := range pods {
							key := pod.Namespace + "/" + pod.Name
							podKeys[key] = true
						}

						for _, expectedPod := range tt.wantPods {
							expectedKey := expectedPod.Namespace + "/" + expectedPod.Name
							require.True(t, podKeys[expectedKey], "expected pod %s not found in list", expectedKey)
						}
					}
				}
			})
		})
	}
}

func TestPodInformer_ListByNamespace(t *testing.T) {
	t.Parallel()

	pod1 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod-1",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			NodeName: "node-1",
		},
	}

	pod2 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod-2",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			NodeName: "node-2",
		},
	}

	pod3 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod-3",
			Namespace: "kube-system",
		},
		Spec: corev1.PodSpec{
			NodeName: "node-1",
		},
	}

	pod4 := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod-4",
			Namespace: "custom-namespace",
		},
		Spec: corev1.PodSpec{
			NodeName: "node-1",
		},
	}

	tests := []struct {
		name        string
		initialPods []*corev1.Pod
		namespace   string
		wantCount   int
		wantPods    []*corev1.Pod
		wantErr     bool
	}{
		{
			name:        "list pods in default namespace",
			initialPods: []*corev1.Pod{pod1, pod2, pod3, pod4},
			namespace:   "default",
			wantCount:   2,
			wantPods:    []*corev1.Pod{pod1, pod2},
			wantErr:     false,
		},
		{
			name:        "list pods in kube-system namespace",
			initialPods: []*corev1.Pod{pod1, pod2, pod3, pod4},
			namespace:   "kube-system",
			wantCount:   1,
			wantPods:    []*corev1.Pod{pod3},
			wantErr:     false,
		},
		{
			name:        "list pods in custom namespace",
			initialPods: []*corev1.Pod{pod1, pod2, pod3, pod4},
			namespace:   "custom-namespace",
			wantCount:   1,
			wantPods:    []*corev1.Pod{pod4},
			wantErr:     false,
		},
		{
			name:        "list pods in empty namespace",
			initialPods: []*corev1.Pod{pod1, pod2},
			namespace:   "empty-namespace",
			wantCount:   0,
			wantPods:    nil,
			wantErr:     false,
		},
		{
			name:        "list pods with empty cache",
			initialPods: nil,
			namespace:   "default",
			wantCount:   0,
			wantPods:    nil,
			wantErr:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				var initialObjects []runtime.Object
				for _, pod := range tt.initialPods {
					initialObjects = append(initialObjects, pod)
				}

				clientSet := fake.NewClientset(initialObjects...)
				log := logrus.New()

				infMgr := NewManager(log, clientSet, 10*time.Minute, EnablePodInformer())

				ctx, cancel := context.WithCancel(t.Context())
				t.Cleanup(func() {
					cancel()
				})

				go func() {
					_ = infMgr.Start(ctx)
				}()
				synctest.Wait()

				pods, err := infMgr.Pods().ListByNamespace(tt.namespace)

				if tt.wantErr {
					require.Error(t, err)
				} else {
					require.NoError(t, err)
					require.Len(t, pods, tt.wantCount)

					// Verify that all returned pods are in the specified namespace
					for _, pod := range pods {
						require.Equal(t, tt.namespace, pod.Namespace)
					}

					// Verify that the returned pods match the expected pods
					if tt.wantCount > 0 {
						podNames := make(map[string]bool)
						for _, pod := range pods {
							podNames[pod.Name] = true
						}

						for _, expectedPod := range tt.wantPods {
							require.True(t, podNames[expectedPod.Name], "expected pod %s not found in namespace %s", expectedPod.Name, tt.namespace)
						}
					}
				}
			})
		})
	}
}
