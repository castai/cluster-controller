package k8sclient

import (
	"context"
	"errors"
	"fmt"
	"github.com/castai/cluster-controller/internal/waitext"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apitypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"time"
)

func (k *Client) PatchNode(ctx context.Context, name string, data []byte) error {
	_, err := k.CoreV1().Nodes().Patch(ctx, name, apitypes.StrategicMergePatchType, data, metav1.PatchOptions{})
	if err != nil {
		return fmt.Errorf("patching node: %w", err)
	}

	return nil
}

func (k *Client) PatchNodeStatus(ctx context.Context, name string, patch []byte) error {
	_, err := k.CoreV1().Nodes().PatchStatus(ctx, name, patch)
	if err != nil {
		return fmt.Errorf("patching node status: %w", err)
	}

	return nil
}

func (k *Client) DeleteNode(ctx context.Context, name string) error {
	return k.CoreV1().Nodes().Delete(ctx, name, metav1.DeleteOptions{})
}

func (c *Client) NodesWatcherRun(ctx context.Context) {
	var w watch.Interface
	var err error
	b := waitext.DefaultExponentialBackoff()
	err = waitext.Retry(
		ctx,
		b,
		waitext.Forever,
		func(ctx context.Context) (bool, error) {
			w, err = c.getNodeWatcher(ctx)
			// Context canceled is when the cluster-controller is stopped.
			// In that case context.Canceled is not an error.
			if errors.Is(err, context.Canceled) {
				return false, err
			}
			if err != nil {
				return true, fmt.Errorf("getWatcher: %w", err)
			}
			return false, nil
		},
		func(err error) {
			c.log.Warnf("retrying: %v", err)
		},
	)
	if err != nil {
		c.log.Warnf("finished: %v", err)
		return
	}

	defer w.Stop()

	c.cleanNodes()
	c.log.Info("watching for nodes")

	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Minute):
			go c.NodesWatcherRun(ctx) // start over every 10m for avoiding cleaning list of nodes
			return

		case event, ok := <-w.ResultChan():
			if !ok {
				c.log.Info("watcher closed")
				go c.NodesWatcherRun(ctx) // start over in case of any error.
				return
			}
			err := c.handleNode(event)
			if err != nil {
				c.log.Warnf("failed to handle node event: %v", err)
			}
		}
	}
}

func (c *Client) getNodeWatcher(ctx context.Context) (watch.Interface, error) {
	w, err := c.CoreV1().Nodes().Watch(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("fail to node watching client: %w", err)
	}
	return w, nil
}

func (c *Client) GetNode(name string) (*v1.Node, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	n, ok := c.nodes[name]
	if !ok {
		return nil, fmt.Errorf("node %q not found", name)
	}
	return n, nil
}

func (c *Client) addNode(n *v1.Node) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nodes[n.Name] = n
}

func (c *Client) cleanNodes() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nodes = make(map[string]*v1.Node)
}

var errNodeObject = errors.New("unexpected type for node object")

func (c *Client) handleNode(event watch.Event) error {
	n, ok := event.Object.(*v1.Node)
	if !ok {
		return errNodeObject
	}

	c.addNode(n)

	return nil
}
