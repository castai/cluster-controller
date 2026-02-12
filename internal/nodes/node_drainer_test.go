package nodes

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	ktest "k8s.io/client-go/testing"

	"github.com/castai/cluster-controller/internal/k8s"
)

const (
	testNodeName      = "test-node"
	testCastNamespace = "cast-namespace"
)

// fakePodInformer implements informer.PodInformer for testing
type fakePodInformer struct {
	mu   sync.RWMutex
	pods map[string][]*v1.Pod // key is nodeName
	err  error
}

func newFakePodInformer() *fakePodInformer {
	return &fakePodInformer{
		pods: make(map[string][]*v1.Pod),
	}
}

func (f *fakePodInformer) Get(namespace, name string) (*v1.Pod, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if f.err != nil {
		return nil, f.err
	}

	for _, nodePods := range f.pods {
		for _, pod := range nodePods {
			if pod.Namespace == namespace && pod.Name == name {
				return pod, nil
			}
		}
	}
	return nil, errors.New("pod not found")
}

func (f *fakePodInformer) List() ([]*v1.Pod, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if f.err != nil {
		return nil, f.err
	}

	var allPods []*v1.Pod
	for _, nodePods := range f.pods {
		allPods = append(allPods, nodePods...)
	}
	return allPods, nil
}

func (f *fakePodInformer) ListByNode(nodeName string) ([]*v1.Pod, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if f.err != nil {
		return nil, f.err
	}

	pods := f.pods[nodeName]
	if pods == nil {
		return []*v1.Pod{}, nil
	}
	return pods, nil
}

func (f *fakePodInformer) ListByNamespace(namespace string) ([]*v1.Pod, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	if f.err != nil {
		return nil, f.err
	}

	var filtered []*v1.Pod
	for _, nodePods := range f.pods {
		for _, pod := range nodePods {
			if pod.Namespace == namespace {
				filtered = append(filtered, pod)
			}
		}
	}
	return filtered, nil
}

// nolint: unparam
func (f *fakePodInformer) addPodToNode(nodeName string, pod *v1.Pod) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.pods[nodeName] = append(f.pods[nodeName], pod)
}

func (f *fakePodInformer) clearPodsFromNode(nodeName string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.pods[nodeName] = nil
}

func (f *fakePodInformer) removeFirstPodFromNode(nodeName string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if len(f.pods[nodeName]) > 0 {
		f.pods[nodeName] = f.pods[nodeName][1:]
	}
}

func (f *fakePodInformer) addObjectToNode(nodeName string, obj any) {
	f.mu.Lock()
	defer f.mu.Unlock()

	pod, ok := obj.(*v1.Pod)
	if ok {
		f.pods[nodeName] = append(f.pods[nodeName], pod)
	}
}

func (f *fakePodInformer) setError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.err = err
}

func TestNewDrainer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  DrainerConfig
	}{
		{
			name: "creates drainer with default config",
			cfg: DrainerConfig{
				PodEvictRetryDelay:            100 * time.Millisecond,
				PodsTerminationWaitRetryDelay: 100 * time.Millisecond,
			},
		},
		{
			name: "creates drainer with zero delays",
			cfg: DrainerConfig{
				PodEvictRetryDelay:            0,
				PodsTerminationWaitRetryDelay: 0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			pods := newFakePodInformer()
			clientset := fake.NewClientset()
			client := k8s.NewClient(clientset, logrus.New())
			log := logrus.New()

			m := NewDrainer(pods, client, log, tt.cfg)

			require.NotNil(t, m)
			require.Implements(t, (*Drainer)(nil), m)
		})
	}
}

func TestDrainer_List(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		setupPods   func(*fakePodInformer)
		wantPodLen  int
		wantErr     bool
		wantErrText string
	}{
		{
			name: "returns empty list when no pods",
			setupPods: func(f *fakePodInformer) {
			},
			wantPodLen: 0,
		},
		{
			name: "returns pods from indexer",
			setupPods: func(f *fakePodInformer) {
				f.addPodToNode(testNodeName, &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{Name: "pod1", Namespace: "default"},
				})
				f.addPodToNode(testNodeName, &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{Name: "pod2", Namespace: "default"},
				})
			},
			wantPodLen: 2,
		},
		{
			name: "returns error when indexer fails",
			setupPods: func(f *fakePodInformer) {
				f.setError(errors.New("indexer error"))
			},
			wantErr:     true,
			wantErrText: "indexer error",
		},
		{
			name: "skips non-pod objects",
			setupPods: func(f *fakePodInformer) {
				f.addPodToNode(testNodeName, &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{Name: "pod1", Namespace: "default"},
				})
				f.addObjectToNode(testNodeName, "not-a-pod")
			},
			wantPodLen: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			podInformer := newFakePodInformer()
			if tt.setupPods != nil {
				tt.setupPods(podInformer)
			}

			clientset := fake.NewClientset()
			client := k8s.NewClient(clientset, logrus.New())
			log := logrus.New()

			m := &drainer{
				pods:   podInformer,
				client: client,
				log:    log,
			}

			pods, err := m.list(context.Background(), testNodeName)

			if tt.wantErr {
				require.Error(t, err)
				if tt.wantErrText != "" {
					require.Contains(t, err.Error(), tt.wantErrText)
				}
			} else {
				require.NoError(t, err)
				require.Len(t, pods, tt.wantPodLen)
			}
		})
	}
}

func TestDrainer_Evict(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                   string
		setupPods              func(*fakePodInformer)
		skipDeletedTimeoutSecs int
		wantErr                bool
		wantIgnoredPodsLen     int
		terminatesImmediately  bool
	}{
		{
			name: "returns empty when no pods to evict",
			setupPods: func(f *fakePodInformer) {
			},
			wantIgnoredPodsLen:    0,
			terminatesImmediately: true,
		},
		{
			name: "returns empty when only daemonset pods",
			setupPods: func(f *fakePodInformer) {
				f.addPodToNode(testNodeName, &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "daemonset-pod",
						Namespace: "default",
						OwnerReferences: []metav1.OwnerReference{
							{
								Kind:       "DaemonSet",
								Controller: boolPtr(true),
							},
						},
					},
				})
			},
			wantIgnoredPodsLen:    0,
			terminatesImmediately: true,
		},
		{
			name: "returns empty when only static pods",
			setupPods: func(f *fakePodInformer) {
				f.addPodToNode(testNodeName, &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "static-pod",
						Namespace: "default",
						OwnerReferences: []metav1.OwnerReference{
							{
								Kind:       "Node",
								Controller: boolPtr(true),
							},
						},
					},
				})
			},
			wantIgnoredPodsLen:    0,
			terminatesImmediately: true,
		},
		{
			name: "returns empty when only completed pods",
			setupPods: func(f *fakePodInformer) {
				f.addPodToNode(testNodeName, &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "completed-pod",
						Namespace: "default",
					},
					Status: v1.PodStatus{
						Phase: v1.PodSucceeded,
					},
				})
			},
			wantIgnoredPodsLen:    0,
			terminatesImmediately: true,
		},
		{
			name: "returns empty when only failed pods",
			setupPods: func(f *fakePodInformer) {
				f.addPodToNode(testNodeName, &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "failed-pod",
						Namespace: "default",
					},
					Status: v1.PodStatus{
						Phase: v1.PodFailed,
					},
				})
			},
			wantIgnoredPodsLen:    0,
			terminatesImmediately: true,
		},
		{
			name: "skips pods deleted longer than timeout",
			setupPods: func(f *fakePodInformer) {
				deletionTime := metav1.NewTime(time.Now().Add(-10 * time.Minute))
				f.addPodToNode(testNodeName, &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "deleted-pod",
						Namespace:         "default",
						DeletionTimestamp: &deletionTime,
					},
					Status: v1.PodStatus{
						Phase: v1.PodRunning,
					},
				})
			},
			skipDeletedTimeoutSecs: 60,
			wantIgnoredPodsLen:     0,
			terminatesImmediately:  true,
		},
		{
			name: "returns error when list fails",
			setupPods: func(f *fakePodInformer) {
				f.setError(errors.New("list failed"))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			podInformer := newFakePodInformer()
			if tt.setupPods != nil {
				tt.setupPods(podInformer)
			}

			clientset := fake.NewClientset()
			client := k8s.NewClient(clientset, logrus.New())
			log := logrus.New()

			m := &drainer{
				pods:   podInformer,
				client: client,
				log:    log,
				cfg: DrainerConfig{
					PodEvictRetryDelay:            10 * time.Millisecond,
					PodsTerminationWaitRetryDelay: 10 * time.Millisecond,
				},
			}

			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()

			ignored, err := m.Evict(ctx, EvictRequest{
				Node:                      testNodeName,
				CastNamespace:             testCastNamespace,
				SkipDeletedTimeoutSeconds: tt.skipDeletedTimeoutSecs,
			})

			if tt.wantErr {
				require.Error(t, err)
			} else if tt.terminatesImmediately {
				require.NoError(t, err)
				require.Len(t, ignored, tt.wantIgnoredPodsLen)
			}
		})
	}
}

func TestDrainer_Drain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                   string
		setupPods              func(*fakePodInformer)
		skipDeletedTimeoutSecs int
		wantErr                bool
		wantIgnoredPodsLen     int
		terminatesImmediately  bool
	}{
		{
			name: "returns empty when no pods to drain",
			setupPods: func(f *fakePodInformer) {
			},
			wantIgnoredPodsLen:    0,
			terminatesImmediately: true,
		},
		{
			name: "returns empty when only daemonset pods",
			setupPods: func(f *fakePodInformer) {
				f.addPodToNode(testNodeName, &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "daemonset-pod",
						Namespace: "default",
						OwnerReferences: []metav1.OwnerReference{
							{
								Kind:       "DaemonSet",
								Controller: boolPtr(true),
							},
						},
					},
				})
			},
			wantIgnoredPodsLen:    0,
			terminatesImmediately: true,
		},
		{
			name: "returns error when list fails",
			setupPods: func(f *fakePodInformer) {
				f.setError(errors.New("list failed"))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			podInformer := newFakePodInformer()
			if tt.setupPods != nil {
				tt.setupPods(podInformer)
			}

			clientset := fake.NewClientset()
			client := k8s.NewClient(clientset, logrus.New())
			log := logrus.New()

			m := &drainer{
				pods:   podInformer,
				client: client,
				log:    log,
				cfg: DrainerConfig{
					PodEvictRetryDelay:            10 * time.Millisecond,
					PodsTerminationWaitRetryDelay: 10 * time.Millisecond,
				},
			}

			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()

			ignored, err := m.Drain(ctx, DrainRequest{
				Node:                      testNodeName,
				CastNamespace:             testCastNamespace,
				SkipDeletedTimeoutSeconds: tt.skipDeletedTimeoutSecs,
				DeleteOptions:             metav1.DeleteOptions{},
			})

			if tt.wantErr {
				require.Error(t, err)
			} else if tt.terminatesImmediately {
				require.NoError(t, err)
				require.Len(t, ignored, tt.wantIgnoredPodsLen)
			}
		})
	}
}

func TestDrainer_WaitTermination(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		setupPods         func(*fakePodInformer)
		simulateTerminate func(*fakePodInformer)
		ignoredPods       []*v1.Pod
		contextTimeout    time.Duration
		wantErr           bool
		wantErrText       string
	}{
		{
			name: "returns immediately when no pods",
			setupPods: func(f *fakePodInformer) {
			},
			contextTimeout: 100 * time.Millisecond,
			wantErr:        false,
		},
		{
			name: "returns immediately when all pods are ignored",
			setupPods: func(f *fakePodInformer) {
				f.addPodToNode(testNodeName, &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{Name: "pod1", Namespace: "default"},
				})
			},
			ignoredPods: []*v1.Pod{
				{ObjectMeta: metav1.ObjectMeta{Name: "pod1", Namespace: "default"}},
			},
			contextTimeout: 100 * time.Millisecond,
			wantErr:        false,
		},
		{
			name: "returns error when context is already canceled",
			setupPods: func(f *fakePodInformer) {
			},
			contextTimeout: 0,
			wantErr:        true,
			wantErrText:    "context canceled",
		},
		{
			name: "returns error when indexer fails",
			setupPods: func(f *fakePodInformer) {
				f.setError(errors.New("indexer error"))
			},
			contextTimeout: 100 * time.Millisecond,
			wantErr:        true,
			wantErrText:    "indexer error",
		},
		{
			name: "waits for single pod to terminate",
			setupPods: func(f *fakePodInformer) {
				f.addPodToNode(testNodeName, &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{Name: "pod1", Namespace: "default"},
				})
			},
			simulateTerminate: func(f *fakePodInformer) {
				go func() {
					time.Sleep(50 * time.Millisecond)
					f.clearPodsFromNode(testNodeName)
				}()
			},
			contextTimeout: 500 * time.Millisecond,
			wantErr:        false,
		},
		{
			name: "waits for multiple pods terminating at different times",
			setupPods: func(f *fakePodInformer) {
				f.addPodToNode(testNodeName, &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{Name: "pod1", Namespace: "default"},
				})
				f.addPodToNode(testNodeName, &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{Name: "pod2", Namespace: "default"},
				})
				f.addPodToNode(testNodeName, &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{Name: "pod3", Namespace: "default"},
				})
			},
			simulateTerminate: func(f *fakePodInformer) {
				go func() {
					time.Sleep(30 * time.Millisecond)
					f.removeFirstPodFromNode(testNodeName)
				}()
				go func() {
					time.Sleep(60 * time.Millisecond)
					f.removeFirstPodFromNode(testNodeName)
				}()
				go func() {
					time.Sleep(90 * time.Millisecond)
					f.clearPodsFromNode(testNodeName)
				}()
			},
			contextTimeout: 500 * time.Millisecond,
			wantErr:        false,
		},
		{
			name: "returns context error when pods remain and timeout",
			setupPods: func(f *fakePodInformer) {
				f.addPodToNode(testNodeName, &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{Name: "pod1", Namespace: "default"},
				})
			},
			contextTimeout: 50 * time.Millisecond,
			wantErr:        true,
			wantErrText:    "context",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				podInformer := newFakePodInformer()
				if tt.setupPods != nil {
					tt.setupPods(podInformer)
				}

				clientset := fake.NewClientset()
				client := k8s.NewClient(clientset, logrus.New())
				log := logrus.New()

				m := &drainer{
					pods:   podInformer,
					client: client,
					log:    log,
					cfg: DrainerConfig{
						PodEvictRetryDelay:            10 * time.Millisecond,
						PodsTerminationWaitRetryDelay: 10 * time.Millisecond,
					},
				}

				var ctx context.Context
				var cancel context.CancelFunc
				if tt.contextTimeout == 0 {
					ctx, cancel = context.WithCancel(context.Background())
					cancel()
				} else {
					ctx, cancel = context.WithTimeout(context.Background(), tt.contextTimeout)
					defer cancel()
				}

				if tt.simulateTerminate != nil {
					tt.simulateTerminate(podInformer)
				}

				err := m.waitTerminaition(ctx, testNodeName, tt.ignoredPods)

				if tt.wantErr {
					require.Error(t, err)
					if tt.wantErrText != "" {
						require.Contains(t, err.Error(), tt.wantErrText)
					}
				} else {
					require.NoError(t, err)
				}
			})
		})
	}
}

func TestDrainer_PrioritizePods(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                   string
		setupPods              func(*fakePodInformer)
		skipDeletedTimeoutSecs int
		wantLen                int
		wantContains           []string
	}{
		{
			name: "returns empty when no evictable pods",
			setupPods: func(f *fakePodInformer) {
				f.addPodToNode(testNodeName, &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "daemonset-pod",
						Namespace: "default",
						OwnerReferences: []metav1.OwnerReference{
							{Kind: "DaemonSet", Controller: boolPtr(true)},
						},
					},
				})
			},
			wantLen: 0,
		},
		{
			name: "includes evictable and cast pods",
			setupPods: func(f *fakePodInformer) {
				f.addPodToNode(testNodeName, &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{Name: "cast-pod", Namespace: testCastNamespace},
					Status:     v1.PodStatus{Phase: v1.PodRunning},
				})
				f.addPodToNode(testNodeName, &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{Name: "regular-pod", Namespace: "default"},
					Status:     v1.PodStatus{Phase: v1.PodRunning},
				})
			},
			wantLen:      2,
			wantContains: []string{"regular-pod", "cast-pod"},
		},
		{
			name: "returns only evictable when no cast pods",
			setupPods: func(f *fakePodInformer) {
				f.addPodToNode(testNodeName, &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{Name: "pod1", Namespace: "default"},
					Status:     v1.PodStatus{Phase: v1.PodRunning},
				})
				f.addPodToNode(testNodeName, &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{Name: "pod2", Namespace: "kube-system"},
					Status:     v1.PodStatus{Phase: v1.PodRunning},
				})
			},
			wantLen:      2,
			wantContains: []string{"pod1", "pod2"},
		},
		{
			name: "includes cast pods only",
			setupPods: func(f *fakePodInformer) {
				f.addPodToNode(testNodeName, &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{Name: "cast-pod1", Namespace: testCastNamespace},
					Status:     v1.PodStatus{Phase: v1.PodRunning},
				})
				f.addPodToNode(testNodeName, &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{Name: "cast-pod2", Namespace: testCastNamespace},
					Status:     v1.PodStatus{Phase: v1.PodRunning},
				})
			},
			wantLen:      2,
			wantContains: []string{"cast-pod1", "cast-pod2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			podInformer := newFakePodInformer()
			if tt.setupPods != nil {
				tt.setupPods(podInformer)
			}

			clientset := fake.NewClientset()
			client := k8s.NewClient(clientset, logrus.New())
			log := logrus.New()

			m := &drainer{
				pods:   podInformer,
				client: client,
				log:    log,
			}

			pods, err := m.list(context.Background(), testNodeName)
			require.NoError(t, err)

			result := m.prioritizePods(pods, testCastNamespace, tt.skipDeletedTimeoutSecs)

			require.Len(t, result, tt.wantLen)
			if len(tt.wantContains) > 0 {
				podNames := make(map[string]int)
				for _, p := range result {
					podNames[p.Name]++
				}
				for _, name := range tt.wantContains {
					require.Contains(t, podNames, name, "Expected pod %s to be in result", name)
				}
			}
		})
	}
}

func TestDrainer_HandleFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		failures     []k8s.PodActionFailure
		wantFailures int
		wantErr      bool
	}{
		{
			name:         "returns nil when no failures",
			failures:     []k8s.PodActionFailure{},
			wantFailures: 0,
			wantErr:      false,
		},
		{
			name: "returns failed pods and error for single failure",
			failures: []k8s.PodActionFailure{
				{
					Pod: &v1.Pod{
						ObjectMeta: metav1.ObjectMeta{Name: "pod1", Namespace: "default"},
					},
					Err: errors.New("eviction failed"),
				},
			},
			wantFailures: 1,
			wantErr:      true,
		},
		{
			name: "returns all failed pods for multiple failures",
			failures: []k8s.PodActionFailure{
				{
					Pod: &v1.Pod{
						ObjectMeta: metav1.ObjectMeta{Name: "pod1", Namespace: "default"},
					},
					Err: errors.New("eviction failed"),
				},
				{
					Pod: &v1.Pod{
						ObjectMeta: metav1.ObjectMeta{Name: "pod2", Namespace: "default"},
					},
					Err: errors.New("timeout"),
				},
			},
			wantFailures: 2,
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			clientset := fake.NewClientset()
			client := k8s.NewClient(clientset, logrus.New())
			log := logrus.New()

			m := &drainer{
				client: client,
				log:    log,
			}

			failed, err := m.handleFailures(context.Background(), "test-action", tt.failures)

			require.Len(t, failed, tt.wantFailures)
			if tt.wantErr {
				require.Error(t, err)
				require.IsType(t, &k8s.PodFailedActionError{}, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// nolint: gocognit
func TestDrainer_TryEvict(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		pods           []*v1.Pod
		existingPods   []*v1.Pod
		enableEviction bool
		shouldSucceed  func(namespace, name string) bool
		wantSuccessful int
		wantFailed     int
		wantError      bool
		contextTimeout time.Duration
	}{
		{
			name:           "returns empty when no pods",
			pods:           []*v1.Pod{},
			existingPods:   []*v1.Pod{},
			enableEviction: false,
			wantError:      true,
		},
		{
			name: "eviction not supported - multiple pods",
			pods: []*v1.Pod{
				{ObjectMeta: metav1.ObjectMeta{Name: "pod1", Namespace: "default"}},
				{ObjectMeta: metav1.ObjectMeta{Name: "pod2", Namespace: "default"}},
			},
			existingPods: []*v1.Pod{
				{ObjectMeta: metav1.ObjectMeta{Name: "pod1", Namespace: "default"}},
				{ObjectMeta: metav1.ObjectMeta{Name: "pod2", Namespace: "default"}},
			},
			enableEviction: false,
			wantError:      true,
		},
		{
			name:           "successfully evicts all pods",
			enableEviction: true,
			pods: []*v1.Pod{
				{ObjectMeta: metav1.ObjectMeta{Name: "pod1", Namespace: "default"}},
				{ObjectMeta: metav1.ObjectMeta{Name: "pod2", Namespace: "default"}},
			},
			existingPods: []*v1.Pod{
				{ObjectMeta: metav1.ObjectMeta{Name: "pod1", Namespace: "default"}},
				{ObjectMeta: metav1.ObjectMeta{Name: "pod2", Namespace: "default"}},
			},
			shouldSucceed:  nil,
			wantSuccessful: 2,
			wantFailed:     0,
			wantError:      false,
		},
		{
			name:           "handles partial eviction failures",
			enableEviction: true,
			pods: []*v1.Pod{
				{ObjectMeta: metav1.ObjectMeta{Name: "pod1", Namespace: "default"}},
				{ObjectMeta: metav1.ObjectMeta{Name: "pod2", Namespace: "default"}},
				{ObjectMeta: metav1.ObjectMeta{Name: "pod3", Namespace: "default"}},
				{ObjectMeta: metav1.ObjectMeta{Name: "pod4", Namespace: "default"}},
			},
			existingPods: []*v1.Pod{
				{ObjectMeta: metav1.ObjectMeta{Name: "pod1", Namespace: "default"}},
				{ObjectMeta: metav1.ObjectMeta{Name: "pod2", Namespace: "default"}},
				{ObjectMeta: metav1.ObjectMeta{Name: "pod3", Namespace: "default"}},
				{ObjectMeta: metav1.ObjectMeta{Name: "pod4", Namespace: "default"}},
			},
			shouldSucceed: func(namespace, name string) bool {
				return name == "pod1" || name == "pod2" // pod3 and pod4 fail
			},
			wantSuccessful: 2,
			wantFailed:     2,
			wantError:      false,
			contextTimeout: 500 * time.Millisecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runTest := func(t *testing.T) {
				clientset := fake.NewClientset()
				for _, pod := range tt.existingPods {
					_, err := clientset.CoreV1().Pods(pod.Namespace).Create(
						context.Background(),
						pod,
						metav1.CreateOptions{},
					)
					require.NoError(t, err)
				}

				if tt.enableEviction {
					addEvictionSupport(clientset)
					if tt.shouldSucceed != nil {
						addEvictionReactor(clientset, tt.shouldSucceed)
					}
				}

				client := k8s.NewClient(clientset, logrus.New())
				log := logrus.New()

				m := &drainer{
					client: client,
					log:    log,
					cfg: DrainerConfig{
						PodEvictRetryDelay: 10 * time.Millisecond,
					},
				}

				ctx := context.Background()
				if tt.contextTimeout > 0 {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, tt.contextTimeout)
					defer cancel()
				}

				successful, failed, err := m.tryEvict(ctx, tt.pods)

				if tt.wantError {
					require.Error(t, err)
					require.Contains(t, err.Error(), "could not find the requested resource")
					return
				}

				require.Equal(t, tt.wantSuccessful, len(successful),
					"Expected %d successful evictions, got %d", tt.wantSuccessful, len(successful))
				require.Equal(t, tt.wantFailed, len(failed),
					"Expected %d failed evictions, got %d", tt.wantFailed, len(failed))

				if tt.wantFailed > 0 {
					require.Error(t, err)
					require.IsType(t, &k8s.PodFailedActionError{}, err)
				} else {
					require.NoError(t, err)
				}
			}

			if tt.contextTimeout > 0 {
				runTest(t)
			} else {
				synctest.Test(t, runTest)
			}
		})
	}
}

func TestDrainer_TryDrain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		pods               []*v1.Pod
		existingPods       []*v1.Pod
		deleteOptions      metav1.DeleteOptions
		wantMinSuccessful  int
		wantMinFailed      int
		wantTotalProcessed int
		wantErr            bool
		wantErrContains    string
	}{
		{
			name:               "returns empty when no pods",
			pods:               []*v1.Pod{},
			existingPods:       []*v1.Pod{},
			deleteOptions:      metav1.DeleteOptions{},
			wantTotalProcessed: 0,
			wantErr:            false,
		},
		{
			name: "successfully deletes pod that exists",
			pods: []*v1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "pod1", Namespace: "default"},
				},
			},
			existingPods: []*v1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "pod1", Namespace: "default"},
				},
			},
			deleteOptions:      metav1.DeleteOptions{},
			wantMinSuccessful:  1,
			wantTotalProcessed: 1,
		},
		{
			name: "handles deletion of non-existent pod",
			pods: []*v1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "missing-pod", Namespace: "default"},
				},
			},
			existingPods:       []*v1.Pod{}, // Pod doesn't exist in cluster
			deleteOptions:      metav1.DeleteOptions{},
			wantTotalProcessed: 1,
		},
		{
			name: "handles partial failures - some pods exist, some don't",
			pods: []*v1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "existing-pod1", Namespace: "default"},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "existing-pod2", Namespace: "default"},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "missing-pod1", Namespace: "default"},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "missing-pod2", Namespace: "default"},
				},
			},
			existingPods: []*v1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "existing-pod1", Namespace: "default"},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "existing-pod2", Namespace: "default"},
				},
			},
			deleteOptions:      metav1.DeleteOptions{},
			wantMinSuccessful:  2,
			wantTotalProcessed: 4,
		},
		{
			name: "uses provided delete options",
			pods: []*v1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "pod1", Namespace: "default"},
				},
			},
			existingPods: []*v1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "pod1", Namespace: "default"},
				},
			},
			deleteOptions: metav1.DeleteOptions{
				GracePeriodSeconds: func() *int64 { v := int64(30); return &v }(),
			},
			wantMinSuccessful:  1,
			wantTotalProcessed: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			clientset := fake.NewClientset()
			for _, pod := range tt.existingPods {
				_, err := clientset.CoreV1().Pods(pod.Namespace).Create(
					context.Background(),
					pod,
					metav1.CreateOptions{},
				)
				require.NoError(t, err)
			}

			client := k8s.NewClient(clientset, logrus.New())
			log := logrus.New()

			m := &drainer{
				client: client,
				log:    log,
				cfg: DrainerConfig{
					PodEvictRetryDelay: 10 * time.Millisecond,
					PodDeleteRetries:   3,
				},
			}

			successful, failed, err := m.tryDrain(context.Background(), tt.pods, tt.deleteOptions)

			if tt.wantErr {
				require.Error(t, err)
				if tt.wantErrContains != "" {
					require.Contains(t, err.Error(), tt.wantErrContains)
				}
				return
			}

			totalProcessed := len(successful) + len(failed)
			require.Equal(t, tt.wantTotalProcessed, totalProcessed,
				"Expected %d total pods processed, got %d successful + %d failed",
				tt.wantTotalProcessed, len(successful), len(failed))

			if tt.wantMinSuccessful > 0 {
				require.GreaterOrEqual(t, len(successful), tt.wantMinSuccessful,
					"Expected at least %d successful deletions", tt.wantMinSuccessful)
			}

			if tt.wantMinFailed > 0 {
				require.GreaterOrEqual(t, len(failed), tt.wantMinFailed,
					"Expected at least %d failed deletions", tt.wantMinFailed)
			}

			if len(failed) > 0 {
				require.Error(t, err)
				require.IsType(t, &k8s.PodFailedActionError{}, err)
			}
		})
	}
}

func boolPtr(b bool) *bool {
	return &b
}

func addEvictionSupport(c *fake.Clientset) {
	podsEviction := metav1.APIResource{
		Name:    "pods/eviction",
		Kind:    "Eviction",
		Group:   "policy",
		Version: "v1",
	}
	coreResources := &metav1.APIResourceList{
		GroupVersion: "v1",
		APIResources: []metav1.APIResource{podsEviction},
	}
	c.Resources = append(c.Resources, coreResources)
}

func addEvictionReactor(c *fake.Clientset, shouldSucceed func(namespace, name string) bool) {
	c.PrependReactor("create", "pods", func(action ktest.Action) (bool, runtime.Object, error) {
		if action.GetSubresource() != "eviction" {
			return false, nil, nil
		}

		createAction, ok := action.(ktest.CreateAction)
		if !ok {
			return false, nil, nil
		}

		eviction, ok := createAction.GetObject().(*policyv1.Eviction)
		if !ok {
			return false, nil, nil
		}

		namespace := eviction.GetNamespace()
		name := eviction.GetName()

		if shouldSucceed(namespace, name) {
			return true, nil, nil
		}

		return true, nil, &apierrors.StatusError{
			ErrStatus: metav1.Status{
				Status:  metav1.StatusFailure,
				Message: "Cannot evict pod as it would violate the pod's disruption budget",
				Reason:  metav1.StatusReasonTooManyRequests,
				Code:    429,
			},
		}
	})
}
