package scenarios

import (
	"context"

	"k8s.io/client-go/kubernetes"

	"github.com/castai/cluster-controller/internal/castai"
)

type Preparation func(ctx context.Context, namespace string, clientset kubernetes.Interface) error

type Cleanup func(ctx context.Context, namespace string, clientset kubernetes.Interface) error

type TestRun func(ctx context.Context, actionChannel chan<- castai.ClusterAction) error

type TestScenario func() (Preparation, Cleanup, TestRun)
