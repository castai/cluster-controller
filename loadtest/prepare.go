package loadtest

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/samber/lo"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"

	"github.com/castai/cluster-controller/internal/castai"
)

func TestStuckDrain(actionChannel chan<- castai.ClusterAction) {
	namespaceForTest := "loadtest"
	ctx := context.Background()
	// TODO: Use custom namespace
	// TODO: Proper cleanup

	// Prepare - create node per drain operation.

	var kubeconfig string
	if home := homedir.HomeDir(); home != "" {
		kubeconfig = filepath.Join(home, ".kube", "config")
	}

	// Build the Kubernetes configuration from the kubeconfig file.
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		panic(fmt.Errorf("failed to build config: %v", err))
	}

	// Create a clientset based on the configuration.
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(fmt.Errorf("failed to create clientset: %v", err))
	}

	// prep clean up
	nodes, err := clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", DefaultKwokMarker, KwokMarkerValue),
	})
	if err != nil {
		panic(fmt.Errorf("failed to list nodes: %v", err))
	}

	for _, n := range nodes.Items {
		err = clientset.CoreV1().Nodes().Delete(ctx, n.Name, metav1.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			panic(err)
		}
	}

	slog.Info(fmt.Sprintf("Deleting namespace %v", namespaceForTest))
	err = clientset.CoreV1().Namespaces().Delete(ctx, namespaceForTest, metav1.DeleteOptions{
		GracePeriodSeconds: lo.ToPtr(int64(0)),
		PropagationPolicy:  lo.ToPtr(metav1.DeletePropagationBackground),
	})
	if err != nil && !apierrors.IsNotFound(err) {
		panic(err)
	}

	for range 300 {
		_, err = clientset.CoreV1().Namespaces().Get(ctx, namespaceForTest, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			break
		}
		time.Sleep(1 * time.Second)
	}

	slog.Info(fmt.Sprintf("Recreating namespace %v", namespaceForTest))
	_, err = clientset.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespaceForTest,
		},
	}, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		panic(err)
	}

	var nodesToEvict []*corev1.Node

	for i := range 500 {
		node := NewKwokNode(KwokConfig{}, fmt.Sprintf("kwok-stuck-drain-%d", i))

		_, err = clientset.CoreV1().Nodes().Create(ctx, node, metav1.CreateOptions{})
		if err != nil {
			panic(err)
		}
		nodesToEvict = append(nodesToEvict, node)

		deployment, pdb := DeploymentWithStuckPDB(fmt.Sprintf("fake-deployment-%s-%d", node.Name, i))
		deployment.ObjectMeta.Namespace = namespaceForTest
		deployment.Spec.Replicas = lo.ToPtr(int32(500))
		pdb.ObjectMeta.Namespace = namespaceForTest

		_, err = clientset.AppsV1().Deployments(namespaceForTest).Create(ctx, deployment, metav1.CreateOptions{})
		if err != nil {
			panic(err)
		}

		_, err = clientset.PolicyV1().PodDisruptionBudgets(namespaceForTest).Create(ctx, pdb, metav1.CreateOptions{})
		if err != nil {
			panic(err)
		}
	}

	slog.Info(fmt.Sprintf("Starting drain action creation"))
	for _, node := range nodesToEvict {
		actionChannel <- castai.ClusterAction{
			ID:        uuid.NewString(),
			CreatedAt: time.Now().UTC(),
			ActionDrainNode: &castai.ActionDrainNode{
				NodeName:            node.Name,
				NodeID:              "",
				DrainTimeoutSeconds: 60,
				Force:               false,
			},
		}
	}
}
