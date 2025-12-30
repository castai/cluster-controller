package actions

import (
	"context"
	"testing"

	"github.com/samber/lo"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"

	"github.com/castai/cluster-controller/internal/castai"
)

func Test_newDeleteHandler(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = appsv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	ctx := context.Background()

	tests := map[string]struct {
		objs   []runtime.Object
		action *castai.ClusterAction
		want   int
		err    error
	}{
		"should return error when action is of a different type": {
			action: &castai.ClusterAction{
				ActionDeleteNode: &castai.ActionDeleteNode{},
			},
			err: newUnexpectedTypeErr(&castai.ActionDeleteNode{}, &castai.ActionDelete{}),
		},
		"should skip if resource not found": {
			action: &castai.ClusterAction{
				ActionDelete: &castai.ActionDelete{
					ID: castai.ObjectID{
						GroupVersionResource: castai.GroupVersionResource{
							Group:    appsv1.SchemeGroupVersion.Group,
							Version:  appsv1.SchemeGroupVersion.Version,
							Resource: "deployments",
						},
						Namespace: lo.ToPtr("default"),
						Name:      "nginx",
					},
				},
			},
			objs: []runtime.Object{
				newDeployment(func(d runtime.Object) {
					d.(*appsv1.Deployment).SetName("nginx-1")
				}),
			},
			want: 1,
		},
		"should delete deployment": {
			action: &castai.ClusterAction{
				ActionDelete: &castai.ActionDelete{
					ID: castai.ObjectID{
						GroupVersionResource: castai.GroupVersionResource{
							Group:    appsv1.SchemeGroupVersion.Group,
							Version:  appsv1.SchemeGroupVersion.Version,
							Resource: "deployments",
						},
						Namespace: lo.ToPtr("default"),
						Name:      "nginx",
					},
				},
			},
			objs: []runtime.Object{
				newDeployment(),
				newDeployment(func(d runtime.Object) {
					d.(*appsv1.Deployment).SetName("nginx-1")
				}),
				newDeployment(func(d runtime.Object) {
					d.(*appsv1.Deployment).SetName("nginx-2")
				}),
			},
			want: 2,
		},
		"should delete resource without namespace": {
			action: &castai.ClusterAction{
				ActionDelete: &castai.ActionDelete{
					ID: castai.ObjectID{
						GroupVersionResource: castai.GroupVersionResource{
							Group:    corev1.SchemeGroupVersion.Group,
							Version:  corev1.SchemeGroupVersion.Version,
							Resource: "nodes",
						},
						Name: "node-1",
					},
				},
			},
			objs: []runtime.Object{
				newNode(func(n *corev1.Node) { n.SetName("node-1") }),
				newNode(func(n *corev1.Node) { n.SetName("node-2") }),
			},
			want: 1,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			log := logrus.New()

			c := fake.NewSimpleDynamicClient(scheme, test.objs...)
			handler := NewDeleteHandler(log, c)
			err := handler.Handle(ctx, test.action)
			if test.err != nil {
				r.Error(err)
				r.Equal(test.err, err)
				return
			}

			r.NoError(err)
			res := c.Resource(schema.GroupVersionResource{
				Group:    test.action.ActionDelete.ID.Group,
				Version:  test.action.ActionDelete.ID.Version,
				Resource: test.action.ActionDelete.ID.Resource,
			})
			list, err := res.List(ctx, metav1.ListOptions{})
			r.NoError(err)
			r.Len(list.Items, test.want)
		})
	}
}

func newNode(opts ...func(n *corev1.Node)) *corev1.Node {
	out := &corev1.Node{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Node",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "node-1",
		},
	}
	for _, opt := range opts {
		opt(out)
	}
	return out
}
