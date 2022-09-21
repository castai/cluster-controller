package actions

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	k8stest "k8s.io/client-go/testing"

	"github.com/castai/cluster-controller/castai"
)

func TestCheckStatus_Deleted(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)

	t.Run("return error when node is not deleted", func(t *testing.T) {
		r := require.New(t)
		nodeName := "node1"
		node := &v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: nodeName,
			},
		}
		clientset := fake.NewSimpleClientset(node)

		h := checkNodeStatusHandler{
			log:       log,
			clientset: clientset,
		}

		req := &castai.ActionCheckNodeStatus{
			NodeName:   "node1",
			NodeStatus: castai.ActionCheckNodeStatus_DELETED,
		}

		err := h.Handle(context.Background(), req)
		r.EqualError(err, "node is not deleted")
	})

	t.Run("handle check successfully when node is not found", func(t *testing.T) {
		r := require.New(t)
		clientset := fake.NewSimpleClientset()

		h := checkNodeStatusHandler{
			log:       log,
			clientset: clientset,
		}

		req := &castai.ActionCheckNodeStatus{
			NodeName:   "node1",
			NodeStatus: castai.ActionCheckNodeStatus_DELETED,
		}

		err := h.Handle(context.Background(), req)
		r.NoError(err)
	})
}

func TestCheckStatus_Ready(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)

	t.Run("return error when node is not found", func(t *testing.T) {
		r := require.New(t)
		clientset := fake.NewSimpleClientset()

		h := checkNodeStatusHandler{
			log:       log,
			clientset: clientset,
		}

		watcher := watch.NewFake()

		clientset.PrependWatchReactor("nodes", k8stest.DefaultWatchReactor(watcher, nil))
		go func() {
			time.Sleep(time.Second)
			watcher.Stop()
		}()

		timeout := int32(1)
		req := &castai.ActionCheckNodeStatus{
			NodeName:           "node1",
			NodeStatus:         castai.ActionCheckNodeStatus_READY,
			WaitTimeoutSeconds: &timeout,
		}

		err := h.Handle(context.Background(), req)
		r.EqualError(err, "timeout waiting for node node1 to become ready")
	})

	t.Run("handle check successfully when node become ready", func(t *testing.T) {
		r := require.New(t)
		nodeName := "node1"
		node := &v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: nodeName,
			},
			Status: v1.NodeStatus{
				Conditions: []v1.NodeCondition{
					{
						Type:   v1.NodeReady,
						Status: v1.ConditionFalse,
					},
				},
			},
		}
		clientset := fake.NewSimpleClientset(node)

		h := checkNodeStatusHandler{
			log:       log,
			clientset: clientset,
		}

		timeout := int32(60)
		req := &castai.ActionCheckNodeStatus{
			NodeName:           "node1",
			NodeStatus:         castai.ActionCheckNodeStatus_READY,
			WaitTimeoutSeconds: &timeout,
		}

		var wg sync.WaitGroup
		wg.Add(2)
		var err error
		go func() {
			err = h.Handle(context.Background(), req)
			wg.Done()
		}()

		go func() {
			time.Sleep(1 * time.Second)
			node.Status.Conditions[0].Status = v1.ConditionTrue
			clientset.CoreV1().Nodes().Update(context.Background(), node, metav1.UpdateOptions{})
			wg.Done()
		}()
		wg.Wait()

		r.NoError(err)
	})

	t.Run("handle error when node is not ready", func(t *testing.T) {
		r := require.New(t)
		nodeName := "node1"
		node := &v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: nodeName,
			},
			Status: v1.NodeStatus{
				Conditions: []v1.NodeCondition{},
			},
		}
		clientset := fake.NewSimpleClientset(node)
		watcher := watch.NewFake()

		clientset.PrependWatchReactor("nodes", k8stest.DefaultWatchReactor(watcher, nil))
		go func() {
			time.Sleep(time.Second)
			watcher.Stop()
		}()

		h := checkNodeStatusHandler{
			log:       log,
			clientset: clientset,
		}

		req := &castai.ActionCheckNodeStatus{
			NodeName:   "node1",
			NodeStatus: castai.ActionCheckNodeStatus_READY,
		}

		err := h.Handle(context.Background(), req)
		r.Error(err)
		r.EqualError(err, "timeout waiting for node node1 to become ready")
	})
}