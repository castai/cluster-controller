package suites

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/samber/lo"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/castai/cluster-controller/e2e/client"
)

type gkeTestSuite struct {
	baseSuite
}

func NewGKETestSuite(t *testing.T, cfg *Config) (*gkeTestSuite, error) {
	client, err := client.CreateClient(cfg.CastAI.APIURL, cfg.CastAI.APIToken, "")
	if err != nil {
		return nil, err
	}

	if err := gkeInitShell(cfg); err != nil {
		return nil, err
	}

	config, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		clientcmd.NewDefaultClientConfigLoadingRules(),
		nil,
	).ClientConfig()
	if err != nil {
		return nil, err
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return &gkeTestSuite{
		baseSuite: baseSuite{
			provider:    "gke",
			clusterName: cfg.ClusterName,

			clusterControllerImageRepository: cfg.ClusterControllerImageRepo,
			clusterControllerImageTag:        cfg.ClusterControllerImageTag,

			castClient: client,
			k8sClient:  clientset,
			t:          t,

			clusterConnectedTimeout: 5 * time.Minute,
			addNodeTimeout:          5 * time.Minute,
			deleteNodeTimeout:       5 * time.Minute,
			cleanupClusterTimeout:   5 * time.Minute,
			drainNodeTimeout:        2 * time.Minute,
		},
	}, nil
}

func (ts *gkeTestSuite) Run(ctx context.Context, t *testing.T) {
	r := require.New(t)

	t.Cleanup(func() {
		if err := ts.cleanupCluster(ctx); err != nil {
			ts.t.Logf("failed to cleanup cluster: %v", err)
		}
	})

	extCluster := ts.onboardCluster(ctx, t)

	helmCmdBuilder := strings.Builder{}
	helmCmdBuilder.WriteString("helm upgrade -n castai-agent cluster-controller castai-helm/castai-cluster-controller --wait")
	helmCmdBuilder.WriteString(" --timeout=1m")
	helmCmdBuilder.WriteString(" --reset-then-reuse-values")
	helmCmdBuilder.WriteString(" --set replicas=1")
	helmCmdBuilder.WriteString(fmt.Sprintf(" --set image.repository=%s", ts.clusterControllerImageRepository))
	helmCmdBuilder.WriteString(fmt.Sprintf(" --set image.tag=%s", ts.clusterControllerImageTag))

	r.NoError(ExecPretty(helmCmdBuilder.String()))

	ts.t.Logf("upgraded cluster controller")

	cleanupTestDeployment, err := ts.setupTestDeployment(ctx)
	r.NoError(err)
	t.Cleanup(func() {
		if err := cleanupTestDeployment(); err != nil {
			ts.t.Logf("failed to cleanup test deployment: %v", err)
		}
	})

	ts.t.Logf("created nginx deployment to schedule on new node")

	node, cleanupNode, err := ts.addNode(ctx, *extCluster.Id, client.ExternalclusterV1NodeConfig{
		InstanceType: "e2-small",
		KubernetesLabels: &client.ExternalclusterV1NodeConfig_KubernetesLabels{
			AdditionalProperties: castNodeSelector,
		},
		KubernetesTaints: &[]client.ExternalclusterV1Taint{
			{Key: castNodeTaintKey, Value: "true", Effect: "NoSchedule"},
		},
	})

	r.NoError(err, "failed to add node")

	t.Cleanup(func() {
		if err := cleanupNode(); err != nil {
			ts.t.Logf("failed to cleanup node %s: %v", *node.Id, err)
		}
	})

	ts.t.Logf("node %s ready", *node.Id)

	r.NoError(backoff.Retry(func() error {
		deployment, err := ts.k8sClient.AppsV1().Deployments("default").Get(ctx, "nginx", metav1.GetOptions{})
		if err != nil {
			return err
		}

		if deployment.Status.ReadyReplicas != 1 {
			return fmt.Errorf("nginx replica not running")
		}

		return nil
	}, backoff.WithMaxRetries(backoff.NewConstantBackOff(5*time.Second), 12)))

	drainResp, err := ts.castClient.ExternalClusterAPIDrainNodeWithResponse(ctx, *extCluster.Id, *node.Id, client.ExternalclusterV1DrainConfig{
		TimeoutSeconds: lo.ToPtr(int32(60)),
	})
	r.NoError(err)
	r.NoError(ts.waitForOperation(ctx, drainResp.JSON200.OperationId, ts.drainNodeTimeout))

	t.Logf("node drained")

	r.NoError(backoff.Retry(func() error {
		deployment, err := ts.k8sClient.AppsV1().Deployments("default").Get(ctx, "nginx", metav1.GetOptions{})
		if err != nil {
			return err
		}

		if deployment.Status.UnavailableReplicas != 1 {
			return fmt.Errorf("nginx replica running")
		}

		return nil
	}, backoff.WithMaxRetries(backoff.NewConstantBackOff(5*time.Second), 12)))

	r.NoError(cleanupNode())

	ts.t.Logf("node deleted")
}

type gcpCreds struct {
	ProjectID string `json:"project_id"`
}

func gkeInitShell(cfg *Config) error {
	fp, err := os.CreateTemp("", "gke-key-")
	if err != nil {
		return err
	}

	if _, err := fp.Write([]byte(cfg.GCP.Credentials)); err != nil {
		return err
	}

	if err := fp.Close(); err != nil {
		return err
	}

	creds := &gcpCreds{}
	if err := json.Unmarshal([]byte(cfg.GCP.Credentials), creds); err != nil {
		return err
	}

	for _, cmd := range []string{
		fmt.Sprintf("gcloud auth activate-service-account --key-file %s", fp.Name()),
		fmt.Sprintf("gcloud config set project %s", creds.ProjectID),
		fmt.Sprintf("gcloud container clusters get-credentials %s --zone %s", cfg.ClusterName, cfg.ClusterRegion),
	} {
		if err := ExecPretty(cmd); err != nil {
			return err
		}
	}

	return nil
}
