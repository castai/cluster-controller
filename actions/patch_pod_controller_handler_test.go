package actions

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/samber/lo"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
	ktest "k8s.io/client-go/testing"

	"github.com/castai/cluster-controller/castai"
)

func TestPatchPodControllerHandler(t *testing.T) {
	tests := map[string]struct {
		action             *castai.ClusterAction
		deployments        []*appsv1.Deployment
		expectedDeployment *appsv1.Deployment
		err                error
	}{
		"should return an error when an unexpected type is passed to the handler": {
			action: &castai.ClusterAction{
				ID:               uuid.New().String(),
				ActionDeleteNode: &castai.ActionDeleteNode{},
			},
			err: fmt.Errorf("unexpected type *castai.ActionDeleteNode for patch pod controller handler"),
		},
		"should not panic when nil is passed as the action": {
			action: &castai.ClusterAction{
				ID:                       uuid.New().String(),
				ActionPatchPodController: nil,
			},
			err: fmt.Errorf("unexpected type <nil> for patch pod controller handler"),
		},
		"should update the deployment container resources when the deployment exists": {
			action: &castai.ClusterAction{
				ID: uuid.New().String(),
				ActionPatchPodController: &castai.ActionPatchPodController{
					PodControllerID: castai.PodControllerID{
						Type:      ControllerTypeDeployment,
						Namespace: "my-namespace",
						Name:      "my-app",
					},
					Containers: []castai.PodContainer{
						{
							Name: "my-container",
							Requests: map[string]string{
								"cpu":    "100m",
								"memory": "100Mi",
							},
							Limits: map[string]string{
								"cpu":    "200m",
								"memory": "200Mi",
							},
						},
					},
				},
			},
			deployments: []*appsv1.Deployment{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "my-app",
						Namespace: "my-namespace",
					},
					Spec: appsv1.DeploymentSpec{
						Template: v1.PodTemplateSpec{
							Spec: v1.PodSpec{
								Containers: []v1.Container{
									{
										Name: "my-container",
										Resources: v1.ResourceRequirements{
											Requests: map[v1.ResourceName]resource.Quantity{
												v1.ResourceCPU:    resource.MustParse("50m"),
												v1.ResourceMemory: resource.MustParse("50Mi"),
											},
											Limits: map[v1.ResourceName]resource.Quantity{
												v1.ResourceCPU:    resource.MustParse("50m"),
												v1.ResourceMemory: resource.MustParse("50Mi"),
											},
										},
									},
								},
							},
						},
					},
				},
			},
			expectedDeployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-app",
					Namespace: "my-namespace",
				},
				Spec: appsv1.DeploymentSpec{
					Template: v1.PodTemplateSpec{
						Spec: v1.PodSpec{
							Containers: []v1.Container{
								{
									Name: "my-container",
									Resources: v1.ResourceRequirements{
										Requests: map[v1.ResourceName]resource.Quantity{
											v1.ResourceCPU:    resource.MustParse("100m"),
											v1.ResourceMemory: resource.MustParse("100Mi"),
										},
										Limits: map[v1.ResourceName]resource.Quantity{
											v1.ResourceCPU:    resource.MustParse("200m"),
											v1.ResourceMemory: resource.MustParse("200Mi"),
										},
									},
								},
							},
						},
					},
				},
			},
		},
		"should remove the deployment container resources when a - prefix is passed in the action": {
			action: &castai.ClusterAction{
				ID: uuid.New().String(),
				ActionPatchPodController: &castai.ActionPatchPodController{
					PodControllerID: castai.PodControllerID{
						Type:      ControllerTypeDeployment,
						Namespace: "my-namespace",
						Name:      "my-app",
					},
					Containers: []castai.PodContainer{
						{
							Name: "my-container",
							Requests: map[string]string{
								"-cpu":    "this can be whatever",
								"-memory": "this can be whatever",
							},
							Limits: map[string]string{
								"-cpu":    "this can be whatever",
								"-memory": "this can be whatever",
							},
						},
					},
				},
			},
			deployments: []*appsv1.Deployment{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "my-app",
						Namespace: "my-namespace",
					},
					Spec: appsv1.DeploymentSpec{
						Template: v1.PodTemplateSpec{
							Spec: v1.PodSpec{
								Containers: []v1.Container{
									{
										Name: "my-container",
										Resources: v1.ResourceRequirements{
											Requests: map[v1.ResourceName]resource.Quantity{
												v1.ResourceCPU:    resource.MustParse("50m"),
												v1.ResourceMemory: resource.MustParse("50Mi"),
											},
											Limits: map[v1.ResourceName]resource.Quantity{
												v1.ResourceCPU:    resource.MustParse("50m"),
												v1.ResourceMemory: resource.MustParse("50Mi"),
											},
										},
									},
								},
							},
						},
					},
				},
			},
			expectedDeployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-app",
					Namespace: "my-namespace",
				},
				Spec: appsv1.DeploymentSpec{
					Template: v1.PodTemplateSpec{
						Spec: v1.PodSpec{
							Containers: []v1.Container{
								{
									Name:      "my-container",
									Resources: v1.ResourceRequirements{},
								},
							},
						},
					},
				},
			},
		},
		"should do nothing with resources that were not in the action": {
			action: &castai.ClusterAction{
				ID: uuid.New().String(),
				ActionPatchPodController: &castai.ActionPatchPodController{
					PodControllerID: castai.PodControllerID{
						Type:      ControllerTypeDeployment,
						Namespace: "my-namespace",
						Name:      "my-app",
					},
					Containers: []castai.PodContainer{
						{
							Name: "my-container",
							Requests: map[string]string{
								"cpu":    "100m",
								"memory": "100Mi",
							},
							Limits: map[string]string{
								"cpu":    "200m",
								"memory": "200Mi",
							},
						},
					},
				},
			},
			deployments: []*appsv1.Deployment{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "my-app",
						Namespace: "my-namespace",
					},
					Spec: appsv1.DeploymentSpec{
						Template: v1.PodTemplateSpec{
							Spec: v1.PodSpec{
								Containers: []v1.Container{
									{
										Name: "my-container",
										Resources: v1.ResourceRequirements{
											Requests: map[v1.ResourceName]resource.Quantity{
												v1.ResourceCPU:                    resource.MustParse("50m"),
												v1.ResourceMemory:                 resource.MustParse("50Mi"),
												v1.ResourceName("nvidia.com/gpu"): resource.MustParse("1"),
											},
											Limits: map[v1.ResourceName]resource.Quantity{
												v1.ResourceCPU:                    resource.MustParse("50m"),
												v1.ResourceMemory:                 resource.MustParse("50Mi"),
												v1.ResourceName("nvidia.com/gpu"): resource.MustParse("1"),
											},
										},
									},
								},
							},
						},
					},
				},
			},
			expectedDeployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-app",
					Namespace: "my-namespace",
				},
				Spec: appsv1.DeploymentSpec{
					Template: v1.PodTemplateSpec{
						Spec: v1.PodSpec{
							Containers: []v1.Container{
								{
									Name: "my-container",
									Resources: v1.ResourceRequirements{
										Requests: map[v1.ResourceName]resource.Quantity{
											v1.ResourceCPU:                    resource.MustParse("100m"),
											v1.ResourceMemory:                 resource.MustParse("100Mi"),
											v1.ResourceName("nvidia.com/gpu"): resource.MustParse("1"),
										},
										Limits: map[v1.ResourceName]resource.Quantity{
											v1.ResourceCPU:                    resource.MustParse("200m"),
											v1.ResourceMemory:                 resource.MustParse("200Mi"),
											v1.ResourceName("nvidia.com/gpu"): resource.MustParse("1"),
										},
									},
								},
							},
						},
					},
				},
			},
		},
		"should update the replicas when they're passed": {
			action: &castai.ClusterAction{
				ID: uuid.New().String(),
				ActionPatchPodController: &castai.ActionPatchPodController{
					PodControllerID: castai.PodControllerID{
						Type:      ControllerTypeDeployment,
						Namespace: "my-namespace",
						Name:      "my-app",
					},
					Replicas: lo.ToPtr[int32](10),
					Containers: []castai.PodContainer{
						{
							Name: "my-container",
							Requests: map[string]string{
								"cpu":    "100m",
								"memory": "100Mi",
							},
							Limits: map[string]string{
								"cpu":    "200m",
								"memory": "200Mi",
							},
						},
					},
				},
			},
			deployments: []*appsv1.Deployment{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "my-app",
						Namespace: "my-namespace",
					},
					Spec: appsv1.DeploymentSpec{
						Replicas: lo.ToPtr[int32](5),
						Template: v1.PodTemplateSpec{
							Spec: v1.PodSpec{
								Containers: []v1.Container{
									{
										Name: "my-container",
										Resources: v1.ResourceRequirements{
											Requests: map[v1.ResourceName]resource.Quantity{
												v1.ResourceCPU:    resource.MustParse("50m"),
												v1.ResourceMemory: resource.MustParse("50Mi"),
											},
											Limits: map[v1.ResourceName]resource.Quantity{
												v1.ResourceCPU:    resource.MustParse("50m"),
												v1.ResourceMemory: resource.MustParse("50Mi"),
											},
										},
									},
								},
							},
						},
					},
				},
			},
			expectedDeployment: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-app",
					Namespace: "my-namespace",
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: lo.ToPtr[int32](10),
					Template: v1.PodTemplateSpec{
						Spec: v1.PodSpec{
							Containers: []v1.Container{
								{
									Name: "my-container",
									Resources: v1.ResourceRequirements{
										Requests: map[v1.ResourceName]resource.Quantity{
											v1.ResourceCPU:    resource.MustParse("100m"),
											v1.ResourceMemory: resource.MustParse("100Mi"),
										},
										Limits: map[v1.ResourceName]resource.Quantity{
											v1.ResourceCPU:    resource.MustParse("200m"),
											v1.ResourceMemory: resource.MustParse("200Mi"),
										},
									},
								},
							},
						},
					},
				},
			},
		},
		"should do nothing when action is for a non-existent deployment": {
			action: &castai.ClusterAction{
				ID: uuid.New().String(),
				ActionPatchPodController: &castai.ActionPatchPodController{
					PodControllerID: castai.PodControllerID{
						Type:      ControllerTypeDeployment,
						Namespace: "my-namespace-that-does-not-exist",
						Name:      "my-app",
					},
					Containers: []castai.PodContainer{
						{
							Name: "my-container",
							Requests: map[string]string{
								"cpu":    "100m",
								"memory": "100Mi",
							},
							Limits: map[string]string{
								"cpu":    "200m",
								"memory": "200Mi",
							},
						},
					},
				},
			},
			deployments: []*appsv1.Deployment{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "my-app",
						Namespace: "my-namespace",
					},
					Spec: appsv1.DeploymentSpec{
						Template: v1.PodTemplateSpec{
							Spec: v1.PodSpec{
								Containers: []v1.Container{
									{
										Name: "my-container",
										Resources: v1.ResourceRequirements{
											Requests: map[v1.ResourceName]resource.Quantity{
												v1.ResourceCPU:                    resource.MustParse("50m"),
												v1.ResourceMemory:                 resource.MustParse("50Mi"),
												v1.ResourceName("nvidia.com/gpu"): resource.MustParse("1"),
											},
											Limits: map[v1.ResourceName]resource.Quantity{
												v1.ResourceCPU:                    resource.MustParse("50m"),
												v1.ResourceMemory:                 resource.MustParse("50Mi"),
												v1.ResourceName("nvidia.com/gpu"): resource.MustParse("1"),
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	for name, test := range tests {
		test := test
		t.Run(name, func(t *testing.T) {
			r := require.New(t)
			ctx := context.Background()
			log := logrus.New()

			clientset := fake.NewSimpleClientset()
			for _, deployment := range test.deployments {
				_ = clientset.Tracker().Add(deployment)
			}

			handler := newPatchPodControllerHandler(log, clientset)
			err := handler.Handle(ctx, test.action)
			if test.err != nil {
				r.EqualError(err, test.err.Error())
				return
			}

			r.NoError(err)
			if test.expectedDeployment == nil {
				patch, found := lo.Find(clientset.Actions(), func(action ktest.Action) bool {
					return action.Matches("patch", "deployments")
				})

				r.False(found, "patch should not have been called, but was %+v", patch)
				return
			}

			found := lo.SomeBy(clientset.Actions(), func(action ktest.Action) bool {
				return action.Matches("patch", "deployments")
			})
			r.True(found, "patch should have been called, but it was not")

			got, err := clientset.AppsV1().
				Deployments(test.expectedDeployment.Namespace).
				Get(ctx, test.expectedDeployment.Name, metav1.GetOptions{})
			r.NoError(err)

			r.Equal(fromPtr(test.expectedDeployment.Spec.Replicas), fromPtr(got.Spec.Replicas))

			for i, gotContainer := range got.Spec.Template.Spec.Containers {
				expected := test.expectedDeployment.Spec.Template.Spec.Containers[i]
				r.Equal(expected, gotContainer)
			}

		})
	}
}

func fromPtr[T any](ptr *T) T {
	if ptr != nil {
		return *ptr
	}

	var t T
	return t
}
