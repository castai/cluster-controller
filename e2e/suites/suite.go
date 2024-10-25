package suites

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/samber/lo"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/castai/cluster-controller/e2e/client"
)

type Config struct {
	LogLevel  string `envconfig:"LOG_LEVEL" default:"debug"`
	LogFormat string `envconfig:"LOG_FORMAT" default:"text"`

	ClusterName   string `envconfig:"CLUSTER_NAME"`
	ClusterRegion string `envconfig:"CLUSTER_REGION"`

	ClusterControllerImageRepo string `envconfig:"CLUSTER_CONTROLLER_IMAGE_REPOSITORY" default:"us-docker.pkg.dev/castai-hub/library/cluster-controller"`
	ClusterControllerImageTag  string `envconfig:"CLUSTER_CONTROLLER_IMAGE_TAG"`

	KubeContext string `envconfig:"KUBECONTEXT"`

	CastAI CastAIConfig
	GCP    GCPConfig
}

type GCPConfig struct {
	Credentials string `envconfig:"GCP_CREDENTIALS"`
}

type CastAIConfig struct {
	APIURL   string `envconfig:"CASTAI_API_URL" default:"https://api.dev-master.cast.ai"`
	APIToken string `envconfig:"CASTAI_API_TOKEN"`
}

var (
	castNodeSelector = map[string]string{
		"cluster-controller-e2e": "true",
	}
	castNodeTaintKey = "cluster-controller-e2e"
)

type baseSuite struct {
	clusterName string
	provider    string

	clusterControllerImageRepository string
	clusterControllerImageTag        string

	t          *testing.T
	castClient *client.ClientWithResponses
	k8sClient  *kubernetes.Clientset

	clusterConnectedTimeout time.Duration
	addNodeTimeout          time.Duration
	deleteNodeTimeout       time.Duration
	cleanupClusterTimeout   time.Duration
	drainNodeTimeout        time.Duration
}

func (suite *baseSuite) waitForOperation(ctx context.Context, id string, timeout time.Duration) error {
	interval := 2 * time.Second
	retries := timeout / interval

	var opErr error
	retryErr := backoff.Retry(func() error {
		resp, err := suite.castClient.OperationsAPIGetOperationWithResponse(ctx, id)
		if err != nil {
			return fmt.Errorf("get operation status: %w", err)
		}

		if resp.StatusCode() < 200 || resp.StatusCode() > 299 {
			return fmt.Errorf("status code is non-success %d", resp.StatusCode())
		}

		if resp.JSON200.Error != nil {
			reason := "no reason"
			details := "no details"
			if resp.JSON200.Error.Reason != nil {
				reason = *resp.JSON200.Error.Reason
			}
			if resp.JSON200.Error.Details != nil {
				details = *resp.JSON200.Error.Details
			}

			opErr = fmt.Errorf("operation not successful: %s: %s", reason, details)
			return nil
		}

		if resp.JSON200.Done == nil || !*resp.JSON200.Done {
			return fmt.Errorf("operation not done")
		}

		return nil
	}, backoff.WithMaxRetries(backoff.NewConstantBackOff(interval), uint64(retries))) //nolint: gosec

	if retryErr != nil {
		return retryErr
	}
	return opErr
}

func (suite *baseSuite) waitForClusterCondition(ctx context.Context, id string, checkFn func(cluster *client.ExternalclusterV1Cluster) bool, timeout time.Duration) error {
	interval := 2 * time.Second
	retries := timeout / interval

	return backoff.Retry(func() error {
		cluster, err := suite.castClient.ExternalClusterAPIGetClusterWithResponse(ctx, id)
		if err != nil {
			return fmt.Errorf("get cluster: %w", err)
		}

		if !checkFn(cluster.JSON200) {
			return fmt.Errorf("cluster failed check")
		}
		return nil
	}, backoff.WithMaxRetries(backoff.NewConstantBackOff(interval), uint64(retries))) //nolint: gosec
}

func (suite *baseSuite) addNode(ctx context.Context, clusterID string, cfg client.ExternalclusterV1NodeConfig) (*client.ExternalclusterV1Node, func() error, error) {
	addNodeResp, err := suite.castClient.ExternalClusterAPIAddNodeWithResponse(ctx, clusterID, cfg)
	if err != nil {
		return nil, nil, err
	}

	if err := suite.waitForOperation(ctx, addNodeResp.JSON200.OperationId, suite.addNodeTimeout); err != nil {
		return nil, nil, err
	}

	cleanupFunc := func() error {
		getNodeResp, err := suite.castClient.ExternalClusterAPIGetNodeWithResponse(ctx, clusterID, addNodeResp.JSON200.NodeId)
		if err != nil {
			return err
		}
		if getNodeResp.StatusCode() == 404 {
			return nil
		}

		resp, err := suite.castClient.ExternalClusterAPIDeleteNodeWithResponse(ctx, clusterID, *getNodeResp.JSON200.Id, &client.ExternalClusterAPIDeleteNodeParams{})
		if err != nil {
			return err
		}
		if resp == nil || resp.JSON200 == nil {
			return nil
		}

		if err := suite.waitForOperation(ctx, *resp.JSON200.OperationId, suite.deleteNodeTimeout); err != nil {
			return err
		}

		return nil
	}

	var node *client.ExternalclusterV1Node

	err = backoff.Retry(func() error {
		n, err := suite.castClient.ExternalClusterAPIGetNodeWithResponse(ctx, clusterID, addNodeResp.JSON200.NodeId)
		if err != nil {
			return err
		}

		if *n.JSON200.State.Phase != "ready" {
			return fmt.Errorf("node not ready, current state %s", *n.JSON200.State.Phase)
		}

		node = n.JSON200
		return nil
	}, backoff.WithMaxRetries(backoff.NewConstantBackOff(10*time.Second), 12))
	if err != nil {
		return nil, cleanupFunc, err
	}

	return node, cleanupFunc, nil
}

func (suite *baseSuite) onboardCluster(ctx context.Context, t *testing.T) *client.ExternalclusterV1Cluster {
	r := require.New(t)

	agentScriptResp, err := suite.castClient.AutoscalerAPIGetAgentScript(ctx, &client.AutoscalerAPIGetAgentScriptParams{
		Provider: lo.ToPtr(client.AutoscalerAPIGetAgentScriptParamsProvider(suite.provider)),
	})

	r.NoError(err)
	defer agentScriptResp.Body.Close()
	r.Equal(http.StatusOK, agentScriptResp.StatusCode)

	agentScript, err := io.ReadAll(agentScriptResp.Body)
	r.NoError(err)
	r.NoError(ExecPrettyWithoutCmd(string(agentScript)))

	var extCluster *client.ExternalclusterV1Cluster

	r.NoError(backoff.Retry(func() error {
		cluster, err := suite.findCluster(ctx)
		if err != nil {
			return err
		}
		if cluster == nil {
			return fmt.Errorf("cluster '%s' not found", suite.clusterName)
		}

		extCluster = cluster
		return nil
	}, backoff.WithMaxRetries(backoff.NewConstantBackOff(5*time.Second), 12)))

	r.NotNil(extCluster, "failed to find cluster %s", suite.clusterName)

	r.NoError(suite.waitForClusterCondition(ctx, *extCluster.Id, func(cluster *client.ExternalclusterV1Cluster) bool {
		return *cluster.AgentStatus == "online"
	}, suite.clusterConnectedTimeout))

	credsScriptResp, err := suite.castClient.ExternalClusterAPIGetCredentialsScriptWithResponse(ctx, *extCluster.Id, &client.ExternalClusterAPIGetCredentialsScriptParams{})
	r.NoError(err)
	r.Equal(http.StatusOK, credsScriptResp.HTTPResponse.StatusCode)

	script := fmt.Sprintf("export CASTAI_IMPERSONATE=true\n%s", *credsScriptResp.JSON200.Script)
	r.NoError(ExecPrettyWithoutCmd(script))

	r.NoError(ExecPretty("kubectl scale deployment -n castai-agent castai-agent --replicas=1")) //nolint:dupword
	r.NoError(ExecPretty("kubectl scale deployment -n castai-agent castai-cluster-controller --replicas=1"))

	r.NoError(suite.waitForClusterCondition(ctx, *extCluster.Id, func(cluster *client.ExternalclusterV1Cluster) bool {
		return *cluster.Status == "ready" && cluster.ReconciledAt != nil
	}, suite.clusterConnectedTimeout))

	return extCluster
}

func (suite *baseSuite) cleanupCluster(ctx context.Context) error {
	cluster, err := suite.findCluster(ctx)
	if err != nil {
		return fmt.Errorf("find cluster: %w", err)
	}
	if cluster == nil {
		return nil
	}

	if cluster.CredentialsId != nil {
		if _, err := suite.castClient.ExternalClusterAPIDisconnectClusterWithResponse(ctx, *cluster.Id, client.ExternalclusterV1DisconnectConfig{
			DeleteProvisionedNodes: lo.ToPtr(true),
		}); err != nil {
			return fmt.Errorf("disconnect cluster: %w", err)
		}

		if err := suite.waitForClusterCondition(ctx, *cluster.Id, func(cluster *client.ExternalclusterV1Cluster) bool {
			return *cluster.AgentStatus == "disconnected"
		}, suite.cleanupClusterTimeout); err != nil {
			return fmt.Errorf("wait for cluster disconnect: %w", err)
		}
	}

	cleanupResp, err := suite.castClient.ExternalClusterAPIGetCleanupScriptWithResponse(ctx, *cluster.Id)
	if err != nil {
		return fmt.Errorf("get cleanup script: %w", err)
	}

	if cleanupResp == nil || cleanupResp.JSON200 == nil || cleanupResp.JSON200.Script == nil {
		return fmt.Errorf("missing cleanup script in response")
	}

	if err := ExecPrettyWithoutCmd(*cleanupResp.JSON200.Script); err != nil {
		return fmt.Errorf("executing cleanup script: %w", err)
	}

	if _, err := suite.castClient.ExternalClusterAPIDeleteClusterWithResponse(ctx, *cluster.Id); err != nil {
		return fmt.Errorf("deleting cluster: %w", err)
	}

	return nil
}

func (suite *baseSuite) findCluster(ctx context.Context) (*client.ExternalclusterV1Cluster, error) {
	resp, err := suite.castClient.ExternalClusterAPIListClustersWithResponse(ctx)
	if err != nil {
		return nil, err
	}

	for _, cluster := range *resp.JSON200.Items {
		if *cluster.Name == suite.clusterName {
			return &cluster, nil
		}
	}

	return nil, fmt.Errorf("cluster not found")
}

func getTestDeployment() *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nginx",
			Namespace: "default",
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: lo.ToPtr(int32(1)),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": "nginx",
				},
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app": "nginx",
					},
				},
				Spec: corev1.PodSpec{
					NodeSelector: castNodeSelector,
					Tolerations: []corev1.Toleration{
						{
							Key:      castNodeTaintKey,
							Operator: corev1.TolerationOpEqual,
							Value:    "true",
							Effect:   "NoSchedule",
						},
					},
					Containers: []corev1.Container{
						{
							Name:  "nginx",
							Image: "nginx",
						},
					},
				},
			},
		},
	}
}
