package actions

import (
	"context"
	"testing"

	"github.com/samber/lo"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	apitypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic/fake"
	client_testing "k8s.io/client-go/testing"

	"github.com/castai/cluster-controller/internal/castai"
)

func TestPatchHandler(t *testing.T) {
	tests := map[string]struct {
		objs   []runtime.Object
		action *castai.ClusterAction
		err    error
	}{
		"should return an error when the action is nil": {
			action: &castai.ClusterAction{},
			err:    newUnexpectedTypeErr(nil, &castai.ActionPatch{}),
		},
		"should return an error when the action is of a different type": {
			action: &castai.ClusterAction{
				ActionDeleteNode: &castai.ActionDeleteNode{},
			},
			err: newUnexpectedTypeErr(&castai.ActionDeleteNode{}, &castai.ActionPatch{}),
		},
		"should forward patch to the api in the request": {
			objs: []runtime.Object{
				&appsv1.Deployment{
					TypeMeta: metav1.TypeMeta{
						Kind:       "Deployment",
						APIVersion: "v1",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "existing-deployment",
						Namespace: "default",
					},
					Spec: appsv1.DeploymentSpec{
						Replicas: lo.ToPtr[int32](10),
					},
				},
			},
			action: &castai.ClusterAction{
				ActionPatch: &castai.ActionPatch{
					ID: castai.ObjectID{
						GroupVersionResource: castai.GroupVersionResource{
							Group:    "apps",
							Version:  "v1",
							Resource: "deployments",
						},
						Namespace: lo.ToPtr("default"),
						Name:      "existing-deployment",
					},
					PatchType: string(apitypes.StrategicMergePatchType),
					Patch:     `{"spec":{"replicas":100}}`,
				},
			},
		},
	}

	for name, test := range tests {
		test := test
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			r := require.New(t)
			ctx := context.Background()
			log := logrus.New()

			scheme := runtime.NewScheme()
			r.NoError(v1.AddToScheme(scheme))
			r.NoError(appsv1.AddToScheme(scheme))
			r.NoError(metav1.AddMetaToScheme(scheme))
			client := fake.NewSimpleDynamicClient(scheme, test.objs...)
			handler := NewPatchHandler(log, client)
			err := handler.Handle(ctx, test.action)
			if test.err != nil {
				r.Error(err)
				r.Equal(test.err, err)
				return
			}
			// Else ignore the error, we actually don't care what the patch does, that's up to api-server to decide.
			// The fake client does not work properly with patching. And it does not aim to replicate the api-server logic.
			// There are ways to work around it, but the test is testing fake code then.
			// For context, here's the PR that attempted to circumvent the issue: https://github.com/kubernetes/kubernetes/pull/78630
			actions := client.Fake.Actions()
			r.Len(actions, 1)
			action, ok := actions[0].(client_testing.PatchAction)
			r.True(ok, "action is not a patch action")
			r.Equal("patch", action.GetVerb())
			r.Equal(test.action.ActionPatch.ID.Resource, action.GetResource().Resource)
			r.Equal(test.action.ActionPatch.ID.Group, action.GetResource().Group)
			r.Equal(test.action.ActionPatch.ID.Version, action.GetResource().Version)
			if test.action.ActionPatch.ID.Namespace != nil {
				r.Equal(*test.action.ActionPatch.ID.Namespace, action.GetNamespace())
			}
			r.Equal(test.action.ActionPatch.ID.Name, action.GetName())
			r.Equal(test.action.ActionPatch.PatchType, string(action.GetPatchType()))
			r.Equal(test.action.ActionPatch.Patch, string(action.GetPatch()))
		})
	}
}
