package nodes

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"

	"github.com/castai/cluster-controller/internal/informer"
	"github.com/castai/cluster-controller/internal/k8s"
)

const (
	testNodeName      = "test-node"
	testCastNamespace = "cast-namespace"
)

// fakeIndexer implements cache.Indexer for testing
type fakeIndexer struct {
	cache.Indexer
	objects map[string][]any
	err     error
}

func newFakeIndexer() *fakeIndexer {
	return &fakeIndexer{
		objects: make(map[string][]any),
	}
}

func (f *fakeIndexer) ByIndex(indexName, indexedValue string) ([]any, error) {
	if f.err != nil {
		return nil, f.err
	}
	key := indexName + "/" + indexedValue
	return f.objects[key], nil
}

func (f *fakeIndexer) addPodToNode(nodeName string, pod *v1.Pod) {
	key := informer.PodIndexerName + "/" + nodeName
	f.objects[key] = append(f.objects[key], pod)
}

func (f *fakeIndexer) clearPodsFromNode(nodeName string) {
	key := informer.PodIndexerName + "/" + nodeName
	f.objects[key] = nil
}

func (f *fakeIndexer) setError(err error) {
	f.err = err
}

func TestNewPodInformer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  ManagerConfig
	}{
		{
			name: "creates manager with default config",
			cfg: ManagerConfig{
				podEvictRetryDelay:            100 * time.Millisecond,
				podsTerminationWaitRetryDelay: 100 * time.Millisecond,
			},
		},
		{
			name: "creates manager with zero delays",
			cfg: ManagerConfig{
				podEvictRetryDelay:            0,
				podsTerminationWaitRetryDelay: 0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			indexer := newFakeIndexer()
			clientset := fake.NewClientset()
			client := k8s.NewClient(clientset, logrus.New())
			log := logrus.New()

			m := NewManager(indexer, client, log, tt.cfg)

			require.NotNil(t, m)
			require.Implements(t, (*Manager)(nil), m)
		})
	}
}

func TestManager_List(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		setupPods   func(*fakeIndexer)
		wantPodLen  int
		wantErr     bool
		wantErrText string
	}{
		{
			name: "returns empty list when no pods",
			setupPods: func(f *fakeIndexer) {
				// no pods
			},
			wantPodLen: 0,
		},
		{
			name: "returns pods from indexer",
			setupPods: func(f *fakeIndexer) {
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
			setupPods: func(f *fakeIndexer) {
				f.setError(errors.New("indexer error"))
			},
			wantErr:     true,
			wantErrText: "indexer error",
		},
		{
			name: "skips non-pod objects",
			setupPods: func(f *fakeIndexer) {
				f.addPodToNode(testNodeName, &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{Name: "pod1", Namespace: "default"},
				})
				// Add a non-pod object (this shouldn't happen in practice, but tests defensive coding)
				key := informer.PodIndexerName + "/" + testNodeName
				f.objects[key] = append(f.objects[key], "not-a-pod")
			},
			wantPodLen: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			indexer := newFakeIndexer()
			if tt.setupPods != nil {
				tt.setupPods(indexer)
			}

			clientset := fake.NewClientset()
			client := k8s.NewClient(clientset, logrus.New())
			log := logrus.New()

			m := &manager{
				indexer: indexer,
				client:  client,
				log:     log,
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

func TestManager_Evict(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                   string
		setupPods              func(*fakeIndexer)
		skipDeletedTimeoutSecs int
		wantErr                bool
		wantIgnoredPodsLen     int
		terminatesImmediately  bool
	}{
		{
			name: "returns empty when no pods to evict",
			setupPods: func(f *fakeIndexer) {
				// no pods
			},
			wantIgnoredPodsLen:    0,
			terminatesImmediately: true,
		},
		{
			name: "returns empty when only daemonset pods",
			setupPods: func(f *fakeIndexer) {
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
			setupPods: func(f *fakeIndexer) {
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
			setupPods: func(f *fakeIndexer) {
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
			setupPods: func(f *fakeIndexer) {
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
			setupPods: func(f *fakeIndexer) {
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
			skipDeletedTimeoutSecs: 60, // 1 minute timeout, pod deleted 10 minutes ago
			wantIgnoredPodsLen:     0,
			terminatesImmediately:  true,
		},
		{
			name: "returns error when list fails",
			setupPods: func(f *fakeIndexer) {
				f.setError(errors.New("list failed"))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			indexer := newFakeIndexer()
			if tt.setupPods != nil {
				tt.setupPods(indexer)
			}

			clientset := fake.NewClientset()
			client := k8s.NewClient(clientset, logrus.New())
			log := logrus.New()

			m := &manager{
				indexer: indexer,
				client:  client,
				log:     log,
				cfg: ManagerConfig{
					podEvictRetryDelay:            10 * time.Millisecond,
					podsTerminationWaitRetryDelay: 10 * time.Millisecond,
				},
			}

			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()

			ignored, err := m.Evict(ctx, testNodeName, testCastNamespace, tt.skipDeletedTimeoutSecs)

			if tt.wantErr {
				require.Error(t, err)
			} else if tt.terminatesImmediately {
				require.NoError(t, err)
				require.Len(t, ignored, tt.wantIgnoredPodsLen)
			}
		})
	}
}

func TestManager_Drain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                   string
		setupPods              func(*fakeIndexer)
		skipDeletedTimeoutSecs int
		wantErr                bool
		wantIgnoredPodsLen     int
		terminatesImmediately  bool
	}{
		{
			name: "returns empty when no pods to drain",
			setupPods: func(f *fakeIndexer) {
				// no pods
			},
			wantIgnoredPodsLen:    0,
			terminatesImmediately: true,
		},
		{
			name: "returns empty when only daemonset pods",
			setupPods: func(f *fakeIndexer) {
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
			setupPods: func(f *fakeIndexer) {
				f.setError(errors.New("list failed"))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			indexer := newFakeIndexer()
			if tt.setupPods != nil {
				tt.setupPods(indexer)
			}

			clientset := fake.NewClientset()
			client := k8s.NewClient(clientset, logrus.New())
			log := logrus.New()

			m := &manager{
				indexer: indexer,
				client:  client,
				log:     log,
				cfg: ManagerConfig{
					podEvictRetryDelay:            10 * time.Millisecond,
					podsTerminationWaitRetryDelay: 10 * time.Millisecond,
				},
			}

			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()

			ignored, err := m.Drain(ctx, testNodeName, testCastNamespace, tt.skipDeletedTimeoutSecs)

			if tt.wantErr {
				require.Error(t, err)
			} else if tt.terminatesImmediately {
				require.NoError(t, err)
				require.Len(t, ignored, tt.wantIgnoredPodsLen)
			}
		})
	}
}

func TestManager_WaitTermination(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		setupPods      func(*fakeIndexer)
		ignoredPods    []*v1.Pod
		contextTimeout time.Duration
		wantErr        bool
		wantErrText    string
	}{
		{
			name: "returns immediately when no pods",
			setupPods: func(f *fakeIndexer) {
				// no pods
			},
			ignoredPods:    nil,
			contextTimeout: 100 * time.Millisecond,
			wantErr:        false,
		},
		{
			name: "returns immediately when all pods are ignored",
			setupPods: func(f *fakeIndexer) {
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
			name: "returns context error when pods remain and context times out",
			setupPods: func(f *fakeIndexer) {
				f.addPodToNode(testNodeName, &v1.Pod{
					ObjectMeta: metav1.ObjectMeta{Name: "pod1", Namespace: "default"},
				})
			},
			ignoredPods:    nil,
			contextTimeout: 50 * time.Millisecond,
			wantErr:        true,
			wantErrText:    "context",
		},
		{
			name: "returns error when context is already canceled",
			setupPods: func(f *fakeIndexer) {
				// no setup needed - context will be canceled
			},
			ignoredPods:    nil,
			contextTimeout: 0, // will be canceled immediately
			wantErr:        true,
			wantErrText:    "context canceled",
		},
		{
			name: "returns error when indexer fails",
			setupPods: func(f *fakeIndexer) {
				f.setError(errors.New("indexer error"))
			},
			ignoredPods:    nil,
			contextTimeout: 100 * time.Millisecond,
			wantErr:        true,
			wantErrText:    "indexer error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			indexer := newFakeIndexer()
			if tt.setupPods != nil {
				tt.setupPods(indexer)
			}

			clientset := fake.NewClientset()
			client := k8s.NewClient(clientset, logrus.New())
			log := logrus.New()

			m := &manager{
				indexer: indexer,
				client:  client,
				log:     log,
				cfg: ManagerConfig{
					podEvictRetryDelay:            10 * time.Millisecond,
					podsTerminationWaitRetryDelay: 10 * time.Millisecond,
				},
			}

			var ctx context.Context
			var cancel context.CancelFunc
			if tt.contextTimeout == 0 {
				ctx, cancel = context.WithCancel(context.Background())
				cancel() // cancel immediately
			} else {
				ctx, cancel = context.WithTimeout(context.Background(), tt.contextTimeout)
				defer cancel()
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
	}
}

func TestManager_WaitTermination_PodsTerminate(t *testing.T) {
	t.Parallel()

	indexer := newFakeIndexer()
	indexer.addPodToNode(testNodeName, &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pod1", Namespace: "default"},
	})

	clientset := fake.NewClientset()
	client := k8s.NewClient(clientset, logrus.New())
	log := logrus.New()

	m := &manager{
		indexer: indexer,
		client:  client,
		log:     log,
		cfg: ManagerConfig{
			podEvictRetryDelay:            10 * time.Millisecond,
			podsTerminationWaitRetryDelay: 10 * time.Millisecond,
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// Simulate pod termination after a short delay
	go func() {
		time.Sleep(50 * time.Millisecond)
		indexer.clearPodsFromNode(testNodeName)
	}()

	err := m.waitTerminaition(ctx, testNodeName, nil)
	require.NoError(t, err)
}

func TestPartitionPodsForEviction(t *testing.T) {
	t.Parallel()

	now := time.Now()
	recentDeletion := metav1.NewTime(now.Add(-30 * time.Second))
	oldDeletion := metav1.NewTime(now.Add(-10 * time.Minute))

	tests := []struct {
		name                     string
		pods                     []v1.Pod
		castNamespace            string
		skipDeletedTimeoutSecs   int
		wantEvictableLen         int
		wantNonEvictableLen      int
		wantCastPodsLen          int
		wantEvictablePodNames    []string
		wantNonEvictablePodNames []string
		wantCastPodNames         []string
	}{
		{
			name:             "empty pods returns empty result",
			pods:             []v1.Pod{},
			castNamespace:    testCastNamespace,
			wantEvictableLen: 0,
		},
		{
			name: "regular pods are evictable",
			pods: []v1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "pod1", Namespace: "default"},
					Status:     v1.PodStatus{Phase: v1.PodRunning},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "pod2", Namespace: "kube-system"},
					Status:     v1.PodStatus{Phase: v1.PodRunning},
				},
			},
			castNamespace:         testCastNamespace,
			wantEvictableLen:      2,
			wantEvictablePodNames: []string{"pod1", "pod2"},
		},
		{
			name: "daemonset pods are non-evictable",
			pods: []v1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "daemonset-pod",
						Namespace: "default",
						OwnerReferences: []metav1.OwnerReference{
							{Kind: "DaemonSet", Controller: boolPtr(true)},
						},
					},
					Status: v1.PodStatus{Phase: v1.PodRunning},
				},
			},
			castNamespace:            testCastNamespace,
			wantNonEvictableLen:      1,
			wantNonEvictablePodNames: []string{"daemonset-pod"},
		},
		{
			name: "static pods are non-evictable",
			pods: []v1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "static-pod",
						Namespace: "default",
						OwnerReferences: []metav1.OwnerReference{
							{Kind: "Node", Controller: boolPtr(true)},
						},
					},
					Status: v1.PodStatus{Phase: v1.PodRunning},
				},
			},
			castNamespace:            testCastNamespace,
			wantNonEvictableLen:      1,
			wantNonEvictablePodNames: []string{"static-pod"},
		},
		{
			name: "succeeded pods are skipped",
			pods: []v1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "completed-pod", Namespace: "default"},
					Status:     v1.PodStatus{Phase: v1.PodSucceeded},
				},
			},
			castNamespace:    testCastNamespace,
			wantEvictableLen: 0,
		},
		{
			name: "failed pods are skipped",
			pods: []v1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "failed-pod", Namespace: "default"},
					Status:     v1.PodStatus{Phase: v1.PodFailed},
				},
			},
			castNamespace:    testCastNamespace,
			wantEvictableLen: 0,
		},
		{
			name: "cast namespace pods are identified separately",
			pods: []v1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "cast-pod", Namespace: testCastNamespace},
					Status:     v1.PodStatus{Phase: v1.PodRunning},
				},
			},
			castNamespace:    testCastNamespace,
			wantEvictableLen: 1, // cast pods are added to evictable
			wantCastPodsLen:  1,
			wantCastPodNames: []string{"cast-pod"},
		},
		{
			name: "recently deleted pods are included",
			pods: []v1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "deleted-pod",
						Namespace:         "default",
						DeletionTimestamp: &recentDeletion,
					},
					Status: v1.PodStatus{Phase: v1.PodRunning},
				},
			},
			castNamespace:          testCastNamespace,
			skipDeletedTimeoutSecs: 60,
			wantEvictableLen:       1,
			wantEvictablePodNames:  []string{"deleted-pod"},
		},
		{
			name: "old deleted pods are skipped",
			pods: []v1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:              "old-deleted-pod",
						Namespace:         "default",
						DeletionTimestamp: &oldDeletion,
					},
					Status: v1.PodStatus{Phase: v1.PodRunning},
				},
			},
			castNamespace:          testCastNamespace,
			skipDeletedTimeoutSecs: 60,
			wantEvictableLen:       0,
		},
		{
			name: "mixed pods are partitioned correctly",
			pods: []v1.Pod{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "regular-pod", Namespace: "default"},
					Status:     v1.PodStatus{Phase: v1.PodRunning},
				},
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "daemonset-pod",
						Namespace: "default",
						OwnerReferences: []metav1.OwnerReference{
							{Kind: "DaemonSet", Controller: boolPtr(true)},
						},
					},
					Status: v1.PodStatus{Phase: v1.PodRunning},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "cast-pod", Namespace: testCastNamespace},
					Status:     v1.PodStatus{Phase: v1.PodRunning},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "completed-pod", Namespace: "default"},
					Status:     v1.PodStatus{Phase: v1.PodSucceeded},
				},
			},
			castNamespace:            testCastNamespace,
			wantEvictableLen:         2, // regular-pod + cast-pod (cast pods are appended)
			wantNonEvictableLen:      1, // daemonset-pod
			wantCastPodsLen:          1, // cast-pod
			wantEvictablePodNames:    []string{"regular-pod", "cast-pod"},
			wantNonEvictablePodNames: []string{"daemonset-pod"},
			wantCastPodNames:         []string{"cast-pod"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := k8s.PartitionPodsForEviction(tt.pods, tt.castNamespace, tt.skipDeletedTimeoutSecs)

			require.Len(t, result.Evictable, tt.wantEvictableLen)
			require.Len(t, result.NonEvictable, tt.wantNonEvictableLen)
			require.Len(t, result.CastPods, tt.wantCastPodsLen)

			if len(tt.wantEvictablePodNames) > 0 {
				evictableNames := make([]string, len(result.Evictable))
				for i, p := range result.Evictable {
					evictableNames[i] = p.Name
				}
				require.ElementsMatch(t, tt.wantEvictablePodNames, evictableNames)
			}

			if len(tt.wantNonEvictablePodNames) > 0 {
				nonEvictableNames := make([]string, len(result.NonEvictable))
				for i, p := range result.NonEvictable {
					nonEvictableNames[i] = p.Name
				}
				require.ElementsMatch(t, tt.wantNonEvictablePodNames, nonEvictableNames)
			}

			if len(tt.wantCastPodNames) > 0 {
				castPodNames := make([]string, len(result.CastPods))
				for i, p := range result.CastPods {
					castPodNames[i] = p.Name
				}
				require.ElementsMatch(t, tt.wantCastPodNames, castPodNames)
			}
		})
	}
}

func TestIsDaemonSetPod(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		pod  *v1.Pod
		want bool
	}{
		{
			name: "returns true for daemonset pod",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{Kind: "DaemonSet", Controller: boolPtr(true)},
					},
				},
			},
			want: true,
		},
		{
			name: "returns false for deployment pod",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{Kind: "ReplicaSet", Controller: boolPtr(true)},
					},
				},
			},
			want: false,
		},
		{
			name: "returns false for pod without owner",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: nil,
				},
			},
			want: false,
		},
		{
			name: "returns false for non-controller daemonset owner",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{Kind: "DaemonSet", Controller: boolPtr(false)},
					},
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := k8s.IsDaemonSetPod(tt.pod)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestIsStaticPod(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		pod  *v1.Pod
		want bool
	}{
		{
			name: "returns true for static pod",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{Kind: "Node", Controller: boolPtr(true)},
					},
				},
			},
			want: true,
		},
		{
			name: "returns false for deployment pod",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: []metav1.OwnerReference{
						{Kind: "ReplicaSet", Controller: boolPtr(true)},
					},
				},
			},
			want: false,
		},
		{
			name: "returns false for pod without owner",
			pod: &v1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					OwnerReferences: nil,
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := k8s.IsStaticPod(tt.pod)
			require.Equal(t, tt.want, got)
		})
	}
}

func boolPtr(b bool) *bool {
	return &b
}
