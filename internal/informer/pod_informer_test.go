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
	manager := NewManager(log, clientset, time.Hour, EnablePodInformer())

	informer := manager.pods.Informer()
	lister := manager.pods.Lister()

	require.NotNil(t, informer)
	require.NotNil(t, lister)
	require.False(t, manager.pods.HasSynced())
}
