package informer

import (
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/kubernetes/fake"
)

func TestPodInformer_Informer(t *testing.T) {
	t.Parallel()

	log := logrus.New()
	clientset := fake.NewClientset()
	manager := NewManager(log, clientset, time.Hour)

	informer := manager.pods.Informer()
	require.NotNil(t, informer)
}

func TestPodInformer_Lister(t *testing.T) {
	t.Parallel()

	log := logrus.New()
	clientset := fake.NewClientset()
	manager := NewManager(log, clientset, time.Hour)

	lister := manager.pods.Lister()
	require.NotNil(t, lister)
}

func TestPodInformer_HasSynced(t *testing.T) {
	t.Parallel()

	log := logrus.New()
	clientset := fake.NewClientset()
	manager := NewManager(log, clientset, time.Hour)

	require.False(t, manager.pods.HasSynced())
}
