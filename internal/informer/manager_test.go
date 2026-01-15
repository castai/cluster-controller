package informer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	authorizationv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestNewManager(t *testing.T) {
	t.Parallel()

	log := logrus.New()
	clientset := fake.NewClientset()

	manager := NewManager(log, clientset, time.Hour, EnableNodeInformer(), EnablePodInformer())

	require.NotNil(t, manager)
	require.NotNil(t, manager.GetFactory())
	require.NotNil(t, manager.GetNodeInformer())
	require.NotNil(t, manager.GetPodLister())
	require.NotNil(t, manager.GetPodInformer())

	// VA getters return nil before Start() because vaAvailable is false by default
	require.False(t, manager.IsVAAvailable())
	require.Nil(t, manager.GetVALister())
	require.Nil(t, manager.GetVAInformer())
	require.Nil(t, manager.GetVAIndexer())
}

func TestManager_Start(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		objects       []runtime.Object
		setupCtx      func(t *testing.T) (context.Context, context.CancelFunc)
		wantErr       bool
		errContains   string
		expectedNodes int
		expectedPods  int
	}{
		{
			name: "success with resources",
			objects: []runtime.Object{
				&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}},
				&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod-1", Namespace: "default"}},
			},
			setupCtx: func(t *testing.T) (context.Context, context.CancelFunc) {
				return context.WithTimeout(t.Context(), 5*time.Second)
			},
			wantErr:       false,
			expectedNodes: 1,
			expectedPods:  1,
		},
		{
			name:    "success with empty cluster",
			objects: nil,
			setupCtx: func(t *testing.T) (context.Context, context.CancelFunc) {
				return context.WithTimeout(t.Context(), 5*time.Second)
			},
			wantErr:       false,
			expectedNodes: 0,
			expectedPods:  0,
		},
		{
			name:    "context canceled",
			objects: nil,
			setupCtx: func(t *testing.T) (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				return ctx, func() {}
			},
			wantErr:     true,
			errContains: "failed to sync node informer cache",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			log := logrus.New()
			log.SetLevel(logrus.ErrorLevel)

			clientset := fake.NewClientset(tt.objects...)
			manager := NewManager(log, clientset, 0, EnableNodeInformer(), EnablePodInformer())

			ctx, cancel := tt.setupCtx(t)
			defer cancel()

			err := manager.Start(ctx)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					require.Contains(t, err.Error(), tt.errContains)
				}
				return
			}

			require.NoError(t, err)
			defer manager.Stop()

			nodes, err := manager.GetPodLister().List(labels.Everything())
			require.NoError(t, err)
			require.Len(t, nodes, tt.expectedNodes)

			pods, err := manager.GetPodLister().List(labels.Everything())
			require.NoError(t, err)
			require.Len(t, pods, tt.expectedPods)
		})
	}
}

func TestManager_Start_AlreadyStarted(t *testing.T) {
	t.Parallel()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	clientset := fake.NewClientset()
	manager := NewManager(log, clientset, 0, EnableNodeInformer(), EnablePodInformer())

	ctx := t.Context()

	err := manager.Start(ctx)
	require.NoError(t, err)
	defer manager.Stop()

	err = manager.Start(ctx)
	require.NoError(t, err)
}

func TestManager_Stop(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(t *testing.T, m *Manager)
	}{
		{
			name:  "stop before start",
			setup: func(t *testing.T, m *Manager) {},
		},
		{
			name: "stop after start",
			setup: func(t *testing.T, m *Manager) {
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				err := m.Start(ctx)
				require.NoError(t, err)
			},
		},
		{
			name: "stop multiple times",
			setup: func(t *testing.T, m *Manager) {
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				err := m.Start(ctx)
				require.NoError(t, err)
				m.Stop()
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			log := logrus.New()
			log.SetLevel(logrus.ErrorLevel)

			clientset := fake.NewClientset()
			manager := NewManager(log, clientset, 0, EnableNodeInformer(), EnablePodInformer())

			tt.setup(t, manager)
			manager.Stop()
		})
	}
}

func TestManager_CacheUpdates(t *testing.T) {
	t.Parallel()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
	}

	clientset := fake.NewClientset(node)
	manager := NewManager(log, clientset, 0, EnableNodeInformer(), EnablePodInformer())

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	err := manager.Start(ctx)
	require.NoError(t, err)
	defer manager.Stop()

	nodes, err := manager.GetNodeLister().List(labels.Everything())
	require.NoError(t, err)
	require.Len(t, nodes, 1)

	node2 := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-2"},
	}
	_, err = clientset.CoreV1().Nodes().Create(t.Context(), node2, metav1.CreateOptions{})
	require.NoError(t, err)

	time.Sleep(100 * time.Millisecond)

	nodes, err = manager.GetNodeLister().List(labels.Everything())
	require.NoError(t, err)
	require.Len(t, nodes, 2)
}

func TestManager_VAInformer(t *testing.T) {
	t.Parallel()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	// Create a VolumeAttachment
	va := &storagev1.VolumeAttachment{
		ObjectMeta: metav1.ObjectMeta{Name: "va-1"},
		Spec: storagev1.VolumeAttachmentSpec{
			Attacher: "test-attacher",
			Source: storagev1.VolumeAttachmentSource{
				PersistentVolumeName: strPtr("pv-1"),
			},
			NodeName: "node-1",
		},
	}

	clientset := fake.NewClientset(va)
	mockVAPermissionsAllowed(clientset)
	manager := NewManager(log, clientset, 0, WithDefaultVANodeNameIndexer())

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	err := manager.Start(ctx)
	require.NoError(t, err)
	defer manager.Stop()

	// VA should be available after successful sync
	require.True(t, manager.IsVAAvailable())

	// Test lister
	vas, err := manager.GetVALister().List(labels.Everything())
	require.NoError(t, err)
	require.Len(t, vas, 1)
	require.Equal(t, "va-1", vas[0].Name)

	// Test indexer with node name lookup
	indexed, err := manager.GetVAIndexer().ByIndex(VANodeNameIndexer, "node-1")
	require.NoError(t, err)
	require.Len(t, indexed, 1)

	// Verify no VAs on non-existent node
	indexed, err = manager.GetVAIndexer().ByIndex(VANodeNameIndexer, "node-2")
	require.NoError(t, err)
	require.Len(t, indexed, 0)
}

func TestManager_VAGetters_WhenUnavailable(t *testing.T) {
	t.Parallel()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	clientset := fake.NewClientset()
	mockVAPermissionsAllowed(clientset)
	manager := NewManager(log, clientset, 0, WithDefaultVANodeNameIndexer())

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	err := manager.Start(ctx)
	require.NoError(t, err)
	defer manager.Stop()

	// Normally VA is available after start
	require.True(t, manager.IsVAAvailable())
	require.NotNil(t, manager.GetVALister())
	require.NotNil(t, manager.GetVAInformer())
	require.NotNil(t, manager.GetVAIndexer())

	// Simulate VA becoming unavailable (e.g., would happen if sync failed)
	manager.vaAvailable = false

	// All VA getters should return nil when unavailable
	require.False(t, manager.IsVAAvailable())
	require.Nil(t, manager.GetVALister())
	require.Nil(t, manager.GetVAInformer())
	require.Nil(t, manager.GetVAIndexer())
}

func TestManager_DisabledInformers(t *testing.T) {
	t.Parallel()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	clientset := fake.NewClientset()
	mockVAPermissionsAllowed(clientset)
	// Create manager WITHOUT enabling node/pod informers
	manager := NewManager(log, clientset, 0, WithDefaultVANodeNameIndexer())

	// Before start - getters should return nil
	require.Nil(t, manager.GetNodeLister())
	require.Nil(t, manager.GetNodeInformer())
	require.Nil(t, manager.GetPodLister())
	require.Nil(t, manager.GetPodInformer())

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	// Start should succeed even with disabled informers
	err := manager.Start(ctx)
	require.NoError(t, err)
	defer manager.Stop()

	// After start - getters should still return nil
	require.Nil(t, manager.GetNodeLister())
	require.Nil(t, manager.GetNodeInformer())
	require.Nil(t, manager.GetPodLister())
	require.Nil(t, manager.GetPodInformer())

	// VA should still work (always enabled)
	require.True(t, manager.IsVAAvailable())
	require.NotNil(t, manager.GetVALister())
	require.NotNil(t, manager.GetVAInformer())
	require.NotNil(t, manager.GetVAIndexer())
}

func strPtr(s string) *string {
	return &s
}

// mockVAPermissionsAllowed sets up a reactor to allow all VA permissions (get, list, watch).
func mockVAPermissionsAllowed(clientset *fake.Clientset) {
	clientset.PrependReactor("create", "selfsubjectaccessreviews",
		func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
			return true, &authorizationv1.SelfSubjectAccessReview{
				Status: authorizationv1.SubjectAccessReviewStatus{
					Allowed: true,
				},
			}, nil
		})
}

func TestManager_checkVAPermissions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		allowedVerbs    map[string]bool // verb -> allowed
		apiError        error           // error to return from API
		wantErr         bool
		wantPermErr     bool   // expect *VAPermissionError
		wantMissingVerb string // expected missing verb in VAPermissionError
		wantErrContains string
	}{
		{
			name: "all permissions granted",
			allowedVerbs: map[string]bool{
				"get":   true,
				"list":  true,
				"watch": true,
			},
			wantErr: false,
		},
		{
			name: "missing get permission",
			allowedVerbs: map[string]bool{
				"get":   false,
				"list":  true,
				"watch": true,
			},
			wantErr:         true,
			wantPermErr:     true,
			wantMissingVerb: "get",
		},
		{
			name: "missing list permission",
			allowedVerbs: map[string]bool{
				"get":   true,
				"list":  false,
				"watch": true,
			},
			wantErr:         true,
			wantPermErr:     true,
			wantMissingVerb: "list",
		},
		{
			name: "missing watch permission",
			allowedVerbs: map[string]bool{
				"get":   true,
				"list":  true,
				"watch": false,
			},
			wantErr:         true,
			wantPermErr:     true,
			wantMissingVerb: "watch",
		},
		{
			name: "no permissions granted - fails fast on get",
			allowedVerbs: map[string]bool{
				"get":   false,
				"list":  false,
				"watch": false,
			},
			wantErr:         true,
			wantPermErr:     true,
			wantMissingVerb: "get",
		},
		{
			name:            "API error",
			allowedVerbs:    map[string]bool{},
			apiError:        errors.New("connection refused"),
			wantErr:         true,
			wantPermErr:     false,
			wantErrContains: "connection refused",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			log := logrus.New()
			log.SetLevel(logrus.ErrorLevel)

			clientset := fake.NewClientset()

			// Mock SelfSubjectAccessReview responses
			clientset.PrependReactor("create", "selfsubjectaccessreviews",
				func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
					if tt.apiError != nil {
						return true, nil, tt.apiError
					}

					createAction := action.(k8stesting.CreateAction)
					sar := createAction.GetObject().(*authorizationv1.SelfSubjectAccessReview)
					verb := sar.Spec.ResourceAttributes.Verb

					allowed, exists := tt.allowedVerbs[verb]
					if !exists {
						allowed = false
					}

					return true, &authorizationv1.SelfSubjectAccessReview{
						Status: authorizationv1.SubjectAccessReviewStatus{
							Allowed: allowed,
						},
					}, nil
				})

			manager := NewManager(log, clientset, 0)

			ctx := t.Context()
			err := manager.checkVAPermissions(ctx)

			if !tt.wantErr {
				require.NoError(t, err)
				return
			}

			require.Error(t, err)

			if tt.wantPermErr {
				var permErr *VAPermissionError
				require.ErrorAs(t, err, &permErr)
				require.Equal(t, tt.wantMissingVerb, permErr.MissingVerb)
			} else {
				var permErr *VAPermissionError
				require.False(t, errors.As(err, &permErr), "expected non-permission error")
				if tt.wantErrContains != "" {
					require.Contains(t, err.Error(), tt.wantErrContains)
				}
			}
		})
	}
}

func TestManager_Start_VAPermissionsDenied(t *testing.T) {
	t.Parallel()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	clientset := fake.NewClientset()

	// Mock SelfSubjectAccessReview to deny watch permission
	clientset.PrependReactor("create", "selfsubjectaccessreviews",
		func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
			createAction := action.(k8stesting.CreateAction)
			sar := createAction.GetObject().(*authorizationv1.SelfSubjectAccessReview)
			verb := sar.Spec.ResourceAttributes.Verb

			// Allow get and list, but deny watch
			allowed := verb == "get" || verb == "list"

			return true, &authorizationv1.SelfSubjectAccessReview{
				Status: authorizationv1.SubjectAccessReviewStatus{
					Allowed: allowed,
				},
			}, nil
		})

	manager := NewManager(log, clientset, 0)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	// Start should succeed (VA failure is graceful)
	err := manager.Start(ctx)
	require.NoError(t, err)
	defer manager.Stop()

	// VA should NOT be available due to missing permissions
	require.False(t, manager.IsVAAvailable())
	require.Nil(t, manager.GetVALister())
	require.Nil(t, manager.GetVAInformer())
	require.Nil(t, manager.GetVAIndexer())
}

func TestManager_Start_VAPermissionsCheckFailed(t *testing.T) {
	t.Parallel()

	log := logrus.New()
	log.SetLevel(logrus.ErrorLevel)

	clientset := fake.NewClientset()

	// Mock SelfSubjectAccessReview to return an error
	clientset.PrependReactor("create", "selfsubjectaccessreviews",
		func(action k8stesting.Action) (handled bool, ret runtime.Object, err error) {
			return true, nil, errors.New("API server unavailable")
		})

	manager := NewManager(log, clientset, 0)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	// Start should succeed (VA failure is graceful)
	err := manager.Start(ctx)
	require.NoError(t, err)
	defer manager.Stop()

	// VA should NOT be available due to API error
	require.False(t, manager.IsVAAvailable())
	require.Nil(t, manager.GetVALister())
	require.Nil(t, manager.GetVAInformer())
	require.Nil(t, manager.GetVAIndexer())
}
