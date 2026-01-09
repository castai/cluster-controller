package informer

import (
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/kubernetes/fake"
)

func TestNodeInformer_Informer(t *testing.T) {
	t.Parallel()

	log := logrus.New()
	clientset := fake.NewClientset()
	manager := NewManager(log, clientset, time.Hour)

	informer := manager.nodes.Informer()
	lister := manager.nodes.Lister()

	require.False(t, manager.nodes.HasSynced())
	require.NotNil(t, informer)
	require.NotNil(t, lister)
}
