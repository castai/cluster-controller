package actions

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

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
				Labels: map[string]string{
					castai.LabelProvisionerCastAINodeID: "old-node-id",
				},
			},
		}
		clientset := fake.NewSimpleClientset(node)

		h := checkNodeStatusHandler{
			log:       log,
			clientset: clientset,
		}

		action := &castai.ClusterAction{
			ID: uuid.New().String(),
			ActionCheckNodeStatus: &castai.ActionCheckNodeStatus{
				NodeName:   "node1",
				NodeStatus: castai.ActionCheckNodeStatus_DELETED,
				NodeID:     "old-node-id",
			},
		}

		err := h.Handle(context.Background(), action)
		r.EqualError(err, "node is not deleted")
	})

	t.Run("return error when node is not deleted with no label (backwards compatibility)", func(t *testing.T) {
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

		action := &castai.ClusterAction{
			ID: uuid.New().String(),
			ActionCheckNodeStatus: &castai.ActionCheckNodeStatus{
				NodeName:   "node1",
				NodeStatus: castai.ActionCheckNodeStatus_DELETED,
				NodeID:     "old-node-id",
			},
		}

		err := h.Handle(context.Background(), action)
		r.EqualError(err, "node is not deleted")
	})

	t.Run("handle check successfully when node is not found", func(t *testing.T) {
		r := require.New(t)
		clientset := fake.NewSimpleClientset()

		h := checkNodeStatusHandler{
			log:       log,
			clientset: clientset,
		}

		action := &castai.ClusterAction{
			ID: uuid.New().String(),
			ActionCheckNodeStatus: &castai.ActionCheckNodeStatus{
				NodeName:   "node1",
				NodeStatus: castai.ActionCheckNodeStatus_DELETED,
				NodeID:     "old-node-id",
			},
		}

		err := h.Handle(context.Background(), action)
		r.NoError(err)
	})

	t.Run("handle check successfully when node name was reused but id mismatch", func(t *testing.T) {
		r := require.New(t)
		node := &v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: "node1",
				Labels: map[string]string{
					castai.LabelProvisionerCastAINodeID: "old-node-id",
				},
			},
		}
		clientset := fake.NewSimpleClientset(node)

		h := checkNodeStatusHandler{
			log:       log,
			clientset: clientset,
		}

		action := &castai.ClusterAction{
			ID: uuid.New().String(),
			ActionCheckNodeStatus: &castai.ActionCheckNodeStatus{
				NodeName:   "node1",
				NodeStatus: castai.ActionCheckNodeStatus_DELETED,
				NodeID:     "im-a-different-node",
			},
		}

		err := h.Handle(context.Background(), action)
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
		action := &castai.ClusterAction{
			ID: uuid.New().String(),
			ActionCheckNodeStatus: &castai.ActionCheckNodeStatus{
				NodeName:           "node1",
				NodeStatus:         castai.ActionCheckNodeStatus_READY,
				WaitTimeoutSeconds: &timeout,
			},
		}

		err := h.Handle(context.Background(), action)
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
		action := &castai.ClusterAction{
			ID: uuid.New().String(),
			ActionCheckNodeStatus: &castai.ActionCheckNodeStatus{
				NodeName:           "node1",
				NodeStatus:         castai.ActionCheckNodeStatus_READY,
				WaitTimeoutSeconds: &timeout,
			},
		}

		var wg sync.WaitGroup
		wg.Add(2)
		var err error
		go func() {
			err = h.Handle(context.Background(), action)
			wg.Done()
		}()

		go func() {
			time.Sleep(1 * time.Second)
			node.Status.Conditions[0].Status = v1.ConditionTrue
			_, _ = clientset.CoreV1().Nodes().Update(context.Background(), node, metav1.UpdateOptions{})
			wg.Done()
		}()
		wg.Wait()

		r.NoError(err)
	})

	t.Run("handle check successfully when node become ready - removed taint", func(t *testing.T) {
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
						Status: v1.ConditionTrue,
					},
				},
			},
			Spec: v1.NodeSpec{
				Taints: []v1.Taint{taintCloudProviderUninitialized},
			},
		}
		clientset := fake.NewSimpleClientset(node)

		h := checkNodeStatusHandler{
			log:       log,
			clientset: clientset,
		}

		timeout := int32(60)
		action := &castai.ClusterAction{
			ID: uuid.New().String(),
			ActionCheckNodeStatus: &castai.ActionCheckNodeStatus{
				NodeName:           "node1",
				NodeStatus:         castai.ActionCheckNodeStatus_READY,
				WaitTimeoutSeconds: &timeout,
			},
		}

		var wg sync.WaitGroup
		wg.Add(2)
		var err error
		go func() {
			err = h.Handle(context.Background(), action)
			wg.Done()
		}()

		go func() {
			time.Sleep(1 * time.Second)
			node.Spec.Taints = []v1.Taint{}
			_, _ = clientset.CoreV1().Nodes().Update(context.Background(), node, metav1.UpdateOptions{})
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

		action := &castai.ClusterAction{
			ID: uuid.New().String(),
			ActionCheckNodeStatus: &castai.ActionCheckNodeStatus{
				NodeName:   "node1",
				NodeStatus: castai.ActionCheckNodeStatus_READY,
			},
		}

		err := h.Handle(context.Background(), action)
		r.Error(err)
		r.EqualError(err, "timeout waiting for node node1 to become ready")
	})

	t.Run("handle check successfully when reusing node names happens and node is replaced", func(t *testing.T) {
		r := require.New(t)
		nodeName := "node1"
		node := &v1.Node{
			ObjectMeta: metav1.ObjectMeta{
				Name: nodeName,
				Labels: map[string]string{
					castai.LabelProvisionerCastAINodeID: "old-node-id",
				},
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
		action := &castai.ClusterAction{
			ID: uuid.New().String(),
			ActionCheckNodeStatus: &castai.ActionCheckNodeStatus{
				NodeName:           "node1",
				NodeStatus:         castai.ActionCheckNodeStatus_READY,
				WaitTimeoutSeconds: &timeout,
				NodeID:             "new-node-id",
			},
		}

		// simulate node replacement
		// 1. node is deleted
		// 2. new node is created with the same name and different id
		// 3. node is ready
		// 4. checkNodeStatusHandler.Handle() is called
		var wg sync.WaitGroup
		wg.Add(2)
		var err error
		go func() {
			err = h.Handle(context.Background(), action)
			wg.Done()
		}()

		go func() {
			time.Sleep(1 * time.Second)
			_ = clientset.CoreV1().Nodes().Delete(context.Background(), nodeName, metav1.DeleteOptions{})

			time.Sleep(1 * time.Second)
			newNode := node.DeepCopy()
			newNode.Labels[castai.LabelProvisionerCastAINodeID] = "new-node-id"

			_, _ = clientset.CoreV1().Nodes().Create(context.Background(), newNode, metav1.CreateOptions{})

			time.Sleep(5 * time.Second)
			newNode.Status.Conditions[0].Status = v1.ConditionTrue
			_, _ = clientset.CoreV1().Nodes().UpdateStatus(context.Background(), newNode, metav1.UpdateOptions{})
			wg.Done()
		}()
		wg.Wait()

		r.NoError(err)
	})
}
