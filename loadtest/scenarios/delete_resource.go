package scenarios

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/samber/lo"
	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/castai/cluster-controller/internal/castai"
)

// DeleteResource will simulate deleting N custom resources (ala workload autoscaler flow).
func DeleteResource(count int, dynamicClient dynamic.Interface, apiextensions apiextensionsclientset.Interface, log *slog.Logger) TestScenario {
	return &deleteResourceScenario{
		resourceCount:       count,
		apiextensionsClient: apiextensions,
		dynamicClient:       dynamicClient,
		log:                 log,
	}
}

type deleteResourceScenario struct {
	resourceCount       int
	apiextensionsClient apiextensionsclientset.Interface
	dynamicClient       dynamic.Interface
	log                 *slog.Logger
}

func (c *deleteResourceScenario) Name() string {
	return "delete resource"
}

func (c *deleteResourceScenario) Preparation(ctx context.Context, namespace string, clientset kubernetes.Interface) error {
	crd := WoopCRD()

	c.log.Info("Creating CRD")
	_, err := c.apiextensionsClient.ApiextensionsV1().CustomResourceDefinitions().Create(context.Background(), crd, v1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create CRD: %v", err)
	}

	// Sometimes it takes a few seconds for CRD to be fully consistent, depending on provider.
	time.Sleep(5 * time.Second)

	c.log.Info("Pre-creating resources")
	resourceGVR := schema.GroupVersionResource{
		Group:    woopStubCRDGroup,
		Version:  "v1",
		Resource: woopStubCRDPlural,
	}
	for i := range c.resourceCount {
		instance := WoopCR(namespace, fmt.Sprintf("delete-resource-%d", i))

		_, err = c.dynamicClient.Resource(resourceGVR).Namespace(namespace).Create(context.Background(), instance, v1.CreateOptions{})
		if err != nil {
			fmt.Printf("Error creating instance %d: %v\n", i, err)
		} else {
			fmt.Printf("Created instance: myresource-%d\n", i)
		}
	}

	return nil
}

func (c *deleteResourceScenario) Cleanup(ctx context.Context, namespace string, clientset kubernetes.Interface) error {
	// Note: we don't delete the CRs as namespace deletion will clean them up, and they are much faster than deployments/pods.

	c.log.Info("Deleting custom resource definition")
	err := c.apiextensionsClient.ApiextensionsV1().CustomResourceDefinitions().Delete(ctx, woopStubCRDName, v1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete CRD: %v", err)
	}

	return nil
}

func (c *deleteResourceScenario) Run(ctx context.Context, namespace string, clientset kubernetes.Interface, executor ActionExecutor) error {
	actions := make([]castai.ClusterAction, 0, c.resourceCount)
	woopGRV := WoopGVR()
	for i := range c.resourceCount {
		actions = append(actions, castai.ClusterAction{
			ID: uuid.NewString(),
			ActionDelete: &castai.ActionDelete{
				ID: castai.ObjectID{
					GroupVersionResource: castai.GroupVersionResource{
						Group:    woopGRV.Group,
						Version:  woopGRV.Version,
						Resource: woopGRV.Resource,
					},
					Name:      fmt.Sprintf("delete-resource-%d", i),
					Namespace: lo.ToPtr(namespace),
				},
			},
		})
	}
	executor.ExecuteActions(ctx, actions)

	return nil
}
