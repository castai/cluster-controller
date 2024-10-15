package actions

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/castai/cluster-controller/internal/types"
)

func Test_newCreateHandler(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = appsv1.AddToScheme(scheme)
	ctx := context.Background()

	now := metav1.Time{Time: time.Date(2024, time.September, 1, 0, 0, 0, 0, time.Local)}
	tests := map[string]struct {
		objs      []runtime.Object
		action    *types.ClusterAction
		convertFn func(i map[string]interface{}) client.Object
		err       error
		want      *appsv1.Deployment
	}{
		"should return error when action is of a different type": {
			action: &types.ClusterAction{
				ActionDeleteNode: &types.ActionDeleteNode{},
			},
			err: newUnexpectedTypeErr(&types.ActionDeleteNode{}, &types.ActionCreate{}),
		},
		"should return error when object is not provided": {
			action: &types.ClusterAction{
				ActionCreate: &types.ActionCreate{
					GroupVersionResource: types.GroupVersionResource{},
				},
			},
			err: errors.New("no object provided"),
		},
		"should create new deployment": {
			action: &types.ClusterAction{
				ActionCreate: &types.ActionCreate{
					GroupVersionResource: types.GroupVersionResource{
						Group:    appsv1.SchemeGroupVersion.Group,
						Version:  appsv1.SchemeGroupVersion.Version,
						Resource: "deployments",
					},
					Object: getObj(t, newDeployment()),
				},
			},
			want: newDeployment(),
			convertFn: func(i map[string]interface{}) client.Object {
				out := &appsv1.Deployment{}
				_ = runtime.DefaultUnstructuredConverter.FromUnstructured(i, out)
				return out
			},
		},
		"should patch already existing resource": {
			action: &types.ClusterAction{
				ActionCreate: &types.ActionCreate{
					GroupVersionResource: types.GroupVersionResource{
						Group:    appsv1.SchemeGroupVersion.Group,
						Version:  appsv1.SchemeGroupVersion.Version,
						Resource: "deployments",
					},
					Object: getObj(t, newDeployment(func(d *appsv1.Deployment) {
						d.Labels = map[string]string{"changed": "true"}
					})),
				},
			},
			objs: []runtime.Object{newDeployment(func(d *appsv1.Deployment) {
				d.CreationTimestamp = now
			})},
			want: newDeployment(func(d *appsv1.Deployment) {
				d.CreationTimestamp = now
				d.Labels = map[string]string{"changed": "true"}
			}),
			convertFn: func(i map[string]interface{}) client.Object {
				out := &appsv1.Deployment{}
				_ = runtime.DefaultUnstructuredConverter.FromUnstructured(i, out)
				return out
			},
		},
		"should not patch already existing resource finalizers": {
			action: &types.ClusterAction{
				ActionCreate: &types.ActionCreate{
					GroupVersionResource: types.GroupVersionResource{
						Group:    appsv1.SchemeGroupVersion.Group,
						Version:  appsv1.SchemeGroupVersion.Version,
						Resource: "deployments",
					},
					Object: getObj(t, newDeployment(func(d *appsv1.Deployment) {
					})),
				},
			},
			objs: []runtime.Object{newDeployment(func(d *appsv1.Deployment) {
				d.CreationTimestamp = now
				d.Finalizers = []string{"autoscaling.cast.ai/recommendation"}
			})},
			want: newDeployment(func(d *appsv1.Deployment) {
				d.CreationTimestamp = now
				d.Finalizers = []string{"autoscaling.cast.ai/recommendation"}
			}),
			convertFn: func(i map[string]interface{}) client.Object {
				out := &appsv1.Deployment{}
				_ = runtime.DefaultUnstructuredConverter.FromUnstructured(i, out)
				return out
			},
		},
	}

	for name, test := range tests {
		test := test
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			log := logrus.New()

			c := fake.NewSimpleDynamicClient(scheme, test.objs...)
			handler := NewCreateHandler(log, c)
			err := handler.Handle(ctx, test.action)
			if test.err != nil {
				r.Error(err)
				r.Equal(test.err, err)
				return
			}

			r.NoError(err)
			res := c.Resource(schema.GroupVersionResource{
				Group:    test.action.ActionCreate.Group,
				Version:  test.action.ActionCreate.Version,
				Resource: test.action.ActionCreate.Resource,
			})
			list, err := res.List(ctx, metav1.ListOptions{})
			r.NoError(err)
			r.Len(list.Items, 1)
			r.Equal(test.want, test.convertFn(list.Items[0].Object))
		})
	}
}

func getObj(t *testing.T, obj runtime.Object) map[string]interface{} {
	t.Helper()
	unstructured, err := runtime.DefaultUnstructuredConverter.ToUnstructured(obj)
	if err != nil {
		t.Error(err)
	}
	return unstructured
}

func newDeployment(opts ...func(d *appsv1.Deployment)) *appsv1.Deployment {
	out := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Deployment",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nginx",
			Namespace: "default",
		},
		Spec: appsv1.DeploymentSpec{
			Template: v1.PodTemplateSpec{},
		},
	}
	for _, opt := range opts {
		opt(out)
	}
	return out
}
