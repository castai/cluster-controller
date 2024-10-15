package mock

import (
	"io"
	"time"

	"helm.sh/helm/v3/pkg/kube"
	"k8s.io/api/core/v1"
	"k8s.io/cli-runtime/pkg/resource"
)

// MockKubeClient mocks Helm KubernetesClient interface
type MockKubeClient struct{}

func (m *MockKubeClient) Create(resources kube.ResourceList) (*kube.Result, error) {
	return nil, nil
}

func (m *MockKubeClient) Wait(resources kube.ResourceList, timeout time.Duration) error {
	return nil
}
func (m *MockKubeClient) WaitWithJobs(resources kube.ResourceList, timeout time.Duration) error {
	return nil
}
func (m *MockKubeClient) Delete(resources kube.ResourceList) (*kube.Result, []error) {
	return nil, nil
}
func (m *MockKubeClient) WatchUntilReady(resources kube.ResourceList, timeout time.Duration) error {
	return nil
}
func (m *MockKubeClient) Update(original, target kube.ResourceList, force bool) (*kube.Result, error) {
	return nil, nil
}

// Build is taken from https://github.com/kubernetes/cli-runtime/blob/master/pkg/resource/builder_example_test.go#L77
func (m *MockKubeClient) Build(reader io.Reader, validate bool) (kube.ResourceList, error) {
	builder := resource.NewLocalBuilder().
		// Helm also builds unstructured
		Unstructured().
		// Provide input via a Reader.
		Stream(reader, "input").
		// Flatten items contained in List objects
		Flatten().
		// Accumulate as many items as possible
		ContinueOnError()

	// Run the builder
	result := builder.Do()

	if err := result.Err(); err != nil {
		return nil, err
	}

	return result.Infos()
}
func (m *MockKubeClient) WaitAndGetCompletedPodPhase(name string, timeout time.Duration) (v1.PodPhase, error) {
	return "mock", nil
}
func (m *MockKubeClient) IsReachable() error {
	return nil
}
