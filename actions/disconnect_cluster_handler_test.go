package actions

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/castai/cluster-controller/types"
)

func TestDisconnectClusterHandler(t *testing.T) {
	r := require.New(t)
	ctx := context.Background()

	ns := "castai-agent"
	node := &v1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: ns,
		},
	}
	clientset := fake.NewSimpleClientset(node)

	action := &types.ClusterAction{
		ID:                      uuid.New().String(),
		ActionDisconnectCluster: &types.ActionDisconnectCluster{},
	}
	handler := newDisconnectClusterHandler(logrus.New(), clientset)

	err := handler.Handle(ctx, action)
	r.NoError(err)

	_, err = clientset.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
	r.Error(err)
	r.True(apierrors.IsNotFound(err))
}
