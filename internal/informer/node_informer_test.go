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
	manager := NewManager(log, clientset, time.Hour, 5*time.Second)

	informer := manager.nodes.Informer()
	require.NotNil(t, informer)
}

func TestNodeInformer_Lister(t *testing.T) {
	t.Parallel()

	log := logrus.New()
	clientset := fake.NewClientset()
	manager := NewManager(log, clientset, time.Hour, 5*time.Second)

	lister := manager.nodes.Lister()
	require.NotNil(t, lister)
}

func TestNodeInformer_HasSynced(t *testing.T) {
	t.Parallel()

	log := logrus.New()
	clientset := fake.NewClientset()
	manager := NewManager(log, clientset, time.Hour, 5*time.Second)

	require.False(t, manager.nodes.HasSynced())
}
