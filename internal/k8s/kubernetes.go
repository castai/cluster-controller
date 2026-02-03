package k8s

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/samber/lo"
	"github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/api/policy/v1beta1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	apitypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"

	"github.com/castai/cluster-controller/internal/castai"
	"github.com/castai/cluster-controller/internal/logger"
	"github.com/castai/cluster-controller/internal/waitext"
)

const (
	minDrainTimeout = 0 // Minimal pod drain timeout.
)

var (
	ErrAction             = errors.New("not valid action")
	ErrNodeNotFound       = errors.New("node not found")
	ErrNodeDoesNotMatch   = fmt.Errorf("node does not match")
	ErrProviderIDMismatch = errors.New("provider ID mismatch")
	ErrNodeWatcherClosed  = fmt.Errorf("node watcher closed, no more events will be received")
)

const (
	DefaultMaxRetriesK8SOperation = 5
)

// Client provides Kubernetes operations with common dependencies.
type Client struct {
	clientset kubernetes.Interface
	log       logrus.FieldLogger
}

// NewClient creates a new K8s client with the given dependencies.
func NewClient(clientset kubernetes.Interface, log logrus.FieldLogger) *Client {
	return &Client{
		clientset: clientset,
		log:       log,
	}
}

// Clientset returns the underlying kubernetes.Interface.
func (c *Client) Clientset() kubernetes.Interface {
	return c.clientset
}

// Log returns the logger.
func (c *Client) Log() logrus.FieldLogger {
	return c.log
}

// PatchNode patches a node with the given change function.
func (c *Client) PatchNode(ctx context.Context, node *v1.Node, changeFn func(*v1.Node)) error {
	logger := logger.FromContext(ctx, c.log)
	oldData, err := json.Marshal(node)
	if err != nil {
		return fmt.Errorf("marshaling old data: %w", err)
	}

	changeFn(node)

	newData, err := json.Marshal(node)
	if err != nil {
		return fmt.Errorf("marshaling new data: %w", err)
	}

	patch, err := strategicpatch.CreateTwoWayMergePatch(oldData, newData, node)
	if err != nil {
		return fmt.Errorf("creating patch for node: %w", err)
	}

	err = waitext.Retry(
		ctx,
		DefaultBackoff(),
		DefaultMaxRetriesK8SOperation,
		func(ctx context.Context) (bool, error) {
			_, err = c.clientset.CoreV1().Nodes().Patch(ctx, node.Name, apitypes.StrategicMergePatchType, patch, metav1.PatchOptions{})
			return true, err
		},
		func(err error) {
			logger.Warnf("patch node, will retry: %v", err)
		},
	)
	if err != nil {
		return fmt.Errorf("patching node: %w", err)
	}

	return nil
}

// PatchNodeStatus patches the status of a node.
func (c *Client) PatchNodeStatus(ctx context.Context, name string, patch []byte) error {
	logger := logger.FromContext(ctx, c.log)

	err := waitext.Retry(
		ctx,
		DefaultBackoff(),
		DefaultMaxRetriesK8SOperation,
		func(ctx context.Context) (bool, error) {
			_, err := c.clientset.CoreV1().Nodes().PatchStatus(ctx, name, patch)
			if k8serrors.IsForbidden(err) {
				// permissions might be of older version that can't patch node/status.
				logger.WithField("node", name).WithError(err).Warn("skip patch node/status")
				return false, nil
			}
			return true, err
		},
		func(err error) {
			logger.Warnf("patch node status, will retry: %v", err)
		},
	)
	if err != nil {
		return fmt.Errorf("patch status: %w", err)
	}
	return nil
}

func (c *Client) CordonNode(ctx context.Context, node *v1.Node) error {
	if node.Spec.Unschedulable {
		return nil
	}

	err := c.PatchNode(ctx, node, func(n *v1.Node) {
		n.Spec.Unschedulable = true
	})
	if err != nil {
		return fmt.Errorf("patching node unschedulable: %w", err)
	}
	return nil
}

// GetNodeByIDs retrieves a node by name and validates its ID and provider ID.
func (c *Client) GetNodeByIDs(ctx context.Context, nodeName, nodeID, providerID string) (*v1.Node, error) {
	if nodeID == "" && providerID == "" {
		return nil, fmt.Errorf("node and provider IDs are empty %w", ErrAction)
	}

	n, err := c.clientset.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil && k8serrors.IsNotFound(err) {
		return nil, ErrNodeNotFound
	}
	if err != nil {
		return nil, err
	}

	if n == nil {
		return nil, ErrNodeNotFound
	}

	if err := IsNodeIDProviderIDValid(n, nodeID, providerID); err != nil {
		return nil, fmt.Errorf("requested node ID %s, provider ID %s for node name: %s %w",
			nodeID, providerID, n.Name, err)
	}

	return n, nil
}

// ExecuteBatchPodActions executes the action for each pod in the list.
// It does internal throttling to avoid spawning a goroutine-per-pod on large lists.
// Returns two sets of pods - the ones that successfully executed the action and the ones that failed.
// actionName might be used to distinguish what is the operation (for logs, debugging, etc.) but is optional.
func (c *Client) ExecuteBatchPodActions(
	ctx context.Context,
	pods []v1.Pod,
	action func(context.Context, v1.Pod) error,
	actionName string,
) ([]*v1.Pod, []PodActionFailure) {
	if actionName == "" {
		actionName = "unspecified"
	}
	logger := logger.FromContext(ctx, c.log).WithField("actionName", actionName)

	if len(pods) == 0 {
		logger.Debug("empty list of pods to execute action against")
		return []*v1.Pod{}, nil
	}

	var (
		parallelTasks      = lo.Clamp(len(pods), 1, 50)
		taskChan           = make(chan v1.Pod, len(pods))
		successfulPodsChan = make(chan *v1.Pod, len(pods))
		failedPodsChan     = make(chan PodActionFailure, len(pods))
		wg                 sync.WaitGroup
	)

	logger.Debugf("Starting %d parallel tasks for %d pods: [%v]", parallelTasks, len(pods), lo.Map(pods, func(t v1.Pod, i int) string {
		return fmt.Sprintf("%s/%s", t.Namespace, t.Name)
	}))

	worker := func(taskChan <-chan v1.Pod) {
		for pod := range taskChan {
			if err := action(ctx, pod); err != nil {
				failedPodsChan <- PodActionFailure{
					ActionName: actionName,
					Pod:        &pod,
					Err:        err,
				}
			} else {
				successfulPodsChan <- &pod
			}
		}
		wg.Done()
	}

	for range parallelTasks {
		wg.Add(1)
		go worker(taskChan)
	}

	for _, pod := range pods {
		taskChan <- pod
	}

	close(taskChan)
	wg.Wait()
	close(failedPodsChan)
	close(successfulPodsChan)

	var successfulPods []*v1.Pod
	for pod := range successfulPodsChan {
		successfulPods = append(successfulPods, pod)
	}

	var failedPods []PodActionFailure
	for failure := range failedPodsChan {
		failedPods = append(failedPods, failure)
	}

	return successfulPods, failedPods
}

// EvictPod evicts a pod from a k8s node. Error handling is based on eviction api documentation:
// https://kubernetes.io/docs/tasks/administer-cluster/safely-drain-node/#the-eviction-api
func (c *Client) EvictPod(ctx context.Context, pod v1.Pod, podEvictRetryDelay time.Duration, version schema.GroupVersion) error {
	logger := logger.FromContext(ctx, c.log)

	b := waitext.NewConstantBackoff(podEvictRetryDelay)
	action := func(ctx context.Context) (bool, error) {
		var err error

		c.log.Debugf("requesting eviction for pod %s/%s", pod.Namespace, pod.Name)
		if version == policyv1.SchemeGroupVersion {
			err = c.clientset.PolicyV1().Evictions(pod.Namespace).Evict(ctx, &policyv1.Eviction{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pod.Name,
					Namespace: pod.Namespace,
				},
			})
		} else {
			err = c.clientset.CoreV1().Pods(pod.Namespace).EvictV1beta1(ctx, &v1beta1.Eviction{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "policy/v1beta1",
					Kind:       "Eviction",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      pod.Name,
					Namespace: pod.Namespace,
				},
			})
		}

		if err != nil {
			// Pod is not found - ignore.
			if k8serrors.IsNotFound(err) {
				return false, nil
			}

			// Pod is misconfigured - stop retry.
			if k8serrors.IsInternalError(err) {
				return false, err
			}
		}

		// Other errors - retry.
		// This includes 429 TooManyRequests (due to throttling) and 429 TooManyRequests + DisruptionBudgetCause (due to violated PDBs)
		// This is done to try and do graceful eviction for as long as possible;
		// it is expected that caller has a timeout that will stop this process if the PDB can never be satisfied.
		// Note: pods only receive SIGTERM signals if they are evicted; if PDB prevents that, the signal will not happen here.
		return true, err
	}
	err := waitext.Retry(ctx, b, waitext.Forever, action, func(err error) {
		logger.Warnf("evict pod %s on node %s in namespace %s, will retry: %v", pod.Name, pod.Spec.NodeName, pod.Namespace, err)
	})
	if err != nil {
		return fmt.Errorf("evicting pod %s in namespace %s: %w", pod.Name, pod.Namespace, err)
	}
	return nil
}

// DeletePod deletes a pod from the cluster.
func (c *Client) DeletePod(ctx context.Context, options metav1.DeleteOptions, pod v1.Pod, podDeleteRetries int, podDeleteRetryDelay time.Duration) error {
	logger := logger.FromContext(ctx, c.log)

	b := waitext.NewConstantBackoff(podDeleteRetryDelay)
	action := func(ctx context.Context) (bool, error) {
		err := c.clientset.CoreV1().Pods(pod.Namespace).Delete(ctx, pod.Name, options)
		if err != nil {
			// Pod is not found - ignore.
			if k8serrors.IsNotFound(err) {
				return false, nil
			}

			// Pod is misconfigured - stop retry.
			if k8serrors.IsInternalError(err) {
				return false, err
			}
		}

		// Other errors - retry.
		return true, err
	}
	err := waitext.Retry(ctx, b, podDeleteRetries, action, func(err error) {
		logger.Warnf("deleting pod %s on node %s in namespace %s, will retry: %v", pod.Name, pod.Spec.NodeName, pod.Namespace, err)
	})
	if err != nil {
		return fmt.Errorf("deleting pod %s in namespace %s: %w", pod.Name, pod.Namespace, err)
	}
	return nil
}

// IsNodeIDProviderIDValid checks if the node's ID and provider ID match the requested ones.
func IsNodeIDProviderIDValid(node *v1.Node, nodeID, providerID string) error {
	if nodeID == "" && providerID == "" {
		// if both node ID and provider ID are empty, we can't validate the node
		return fmt.Errorf("node and provider IDs are empty %w", ErrAction)
	}
	emptyProviderID := providerID == "" || node.Spec.ProviderID == ""

	// validate provider id only if non-empty in request and in Node spec
	// Azure provider: provider id can be empty even if node is Ready
	validProviderID := !emptyProviderID && strings.EqualFold(node.Spec.ProviderID, providerID)

	if nodeID == "" && validProviderID {
		// if node ID is not set in labels, but provider ID is valid, node is valid
		return nil
	}

	currentNodeID, ok := node.Labels[castai.LabelNodeID]
	if ok && currentNodeID != "" {
		if strings.EqualFold(currentNodeID, nodeID) {
			if validProviderID {
				// if node ID matches and provider ID is valid, node is valid
				return nil
			}
			if emptyProviderID {
				// if node ID matches but provider ID is empty, node is valid
				return nil
			}
		}
	}
	if (!ok || currentNodeID == "") && validProviderID {
		// if node ID is not set in labels, but provider ID is valid, node is valid
		return nil
	}

	if !emptyProviderID && node.Spec.ProviderID != providerID {
		// if provider ID is not empty in request and does not match node's provider ID, log err for investigations
		return fmt.Errorf("node %v has provider ID %s, but requested provider ID is %s: %w", node.Name, node.Spec.ProviderID, providerID, ErrProviderIDMismatch)
	}

	// if we reach here, it means that node ID and/or provider ID does not match
	return fmt.Errorf("node %v has ID %s and provider ID %s: %w",
		node.Name, currentNodeID, node.Spec.ProviderID, ErrNodeDoesNotMatch)
}

func DefaultBackoff() wait.Backoff {
	return waitext.NewConstantBackoff(500 * time.Millisecond)
}

type PodFailedActionError struct {
	// Action holds context what was the code trying to do.
	Action string
	// Errors should hold an entry per pod, for which the action failed.
	Errors []error
}

func (p *PodFailedActionError) Error() string {
	return fmt.Sprintf("action %q: %v", p.Action, errors.Join(p.Errors...))
}

func (p *PodFailedActionError) Unwrap() []error {
	return p.Errors
}

type PodActionFailure struct {
	ActionName string
	Pod        *v1.Pod
	Err        error
}

type PartitionResult struct {
	Evictable    []v1.Pod
	NonEvictable []v1.Pod
	CastPods     []v1.Pod
}

func PartitionPodsForEviction(pods []v1.Pod, castNamespace string, skipDeletedTimeoutSeconds int) *PartitionResult {
	castPods := make([]v1.Pod, 0)
	evictable := make([]v1.Pod, 0)
	nonEvictable := make([]v1.Pod, 0)

	for _, p := range pods {
		// Skip pods that have been recently removed.
		if !p.DeletionTimestamp.IsZero() &&
			int(time.Since(p.ObjectMeta.GetDeletionTimestamp().Time).Seconds()) > skipDeletedTimeoutSeconds {
			continue
		}

		// Skip completed pods. Will be removed during node removal.
		if p.Status.Phase == v1.PodSucceeded || p.Status.Phase == v1.PodFailed {
			continue
		}

		if IsDaemonSetPod(&p) || IsStaticPod(&p) {
			nonEvictable = append(nonEvictable, p)
			continue
		}

		if p.Namespace == castNamespace {
			castPods = append(castPods, p)
			continue
		}

		evictable = append(evictable, p)
	}

	evictable = append(evictable, castPods...)
	return &PartitionResult{
		CastPods:     castPods,
		Evictable:    evictable,
		NonEvictable: nonEvictable,
	}
}

func IsDaemonSetPod(p *v1.Pod) bool {
	return IsControlledBy(p, "DaemonSet")
}

func IsStaticPod(p *v1.Pod) bool {
	return IsControlledBy(p, "Node")
}

func IsControlledBy(p *v1.Pod, kind string) bool {
	ctrl := metav1.GetControllerOf(p)

	return ctrl != nil && ctrl.Kind == kind
}

// PatchNode patches a node with the given change function.
func PatchNode(ctx context.Context, log logrus.FieldLogger, clientset kubernetes.Interface, node *v1.Node, changeFn func(*v1.Node)) error {
	return NewClient(clientset, log).PatchNode(ctx, node, changeFn)
}

// PatchNodeStatus patches the status of a node.
func PatchNodeStatus(ctx context.Context, log logrus.FieldLogger, clientset kubernetes.Interface, name string, patch []byte) error {
	return NewClient(clientset, log).PatchNodeStatus(ctx, name, patch)
}

// GetNodeByIDs retrieves a node by name and validates its ID and provider ID.
func GetNodeByIDs(ctx context.Context, clientSet corev1client.NodeInterface, nodeName, nodeID, providerID string, log logrus.FieldLogger) (*v1.Node, error) {
	if nodeID == "" && providerID == "" {
		return nil, fmt.Errorf("node and provider IDs are empty %w", ErrAction)
	}

	n, err := clientSet.Get(ctx, nodeName, metav1.GetOptions{})
	if err != nil && k8serrors.IsNotFound(err) {
		return nil, ErrNodeNotFound
	}
	if err != nil {
		return nil, err
	}

	if n == nil {
		return nil, ErrNodeNotFound
	}

	if err := IsNodeIDProviderIDValid(n, nodeID, providerID); err != nil {
		return nil, fmt.Errorf("requested node ID %s, provider ID %s for node name: %s %w",
			nodeID, providerID, n.Name, err)
	}

	return n, nil
}

// ExecuteBatchPodActions executes the action for each pod in the list.
func ExecuteBatchPodActions(
	ctx context.Context,
	log logrus.FieldLogger,
	pods []v1.Pod,
	action func(context.Context, v1.Pod) error,
	actionName string,
) ([]*v1.Pod, []PodActionFailure) {
	return NewClient(nil, log).ExecuteBatchPodActions(ctx, pods, action, actionName)
}

// EvictPod evicts a pod from a k8s node.
func EvictPod(ctx context.Context, pod v1.Pod, podEvictRetryDelay time.Duration, clientset kubernetes.Interface, log logrus.FieldLogger, version schema.GroupVersion) error {
	return NewClient(clientset, log).EvictPod(ctx, pod, podEvictRetryDelay, version)
}

// DeletePod deletes a pod from the cluster.
func DeletePod(ctx context.Context, options metav1.DeleteOptions, pod v1.Pod, podDeleteRetries int, podDeleteRetryDelay time.Duration, clientset kubernetes.Interface, log logrus.FieldLogger) error {
	return NewClient(clientset, log).DeletePod(ctx, options, pod, podDeleteRetries, podDeleteRetryDelay)
}

func CordonNode(ctx context.Context, log logrus.FieldLogger, clientset kubernetes.Interface, node *v1.Node) error {
	return NewClient(clientset, log).CordonNode(ctx, node)
}

// GetDrainTimeout returns drain timeout adjusted to action creation time.
// the result is clamped between 0s and the requested timeout.
func GetDrainTimeout(action *castai.ClusterAction) time.Duration {
	timeSinceCreated := time.Since(action.CreatedAt)
	requestedTimeout := time.Duration(action.ActionDrainNode.DrainTimeoutSeconds) * time.Second

	drainTimeout := requestedTimeout - timeSinceCreated

	return lo.Clamp(drainTimeout, minDrainTimeout*time.Second, requestedTimeout)
}
