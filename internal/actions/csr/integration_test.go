package csr_test

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"net/url"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	certv1 "k8s.io/api/certificates/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/castai/cluster-controller/internal/actions/csr"
	csrtest "github.com/castai/cluster-controller/internal/actions/csr/test"
)

func TestIntegration(t *testing.T) {
	t.Parallel()
	ctx := context.TODO()

	r := require.New(t)

	// TODO either use manager or don't return it
	_, clientset := newManagerAndClientset(t)
	for _, testcase := range []struct {
		creationTimestamp metav1.Time
		description       string
		emails            []string
		groups            []string
		node              string
		notApproved       bool
		signer            string
		uris              []*url.URL
		usages            []certv1.KeyUsage
		username          string
	}{
		{
			description: "[client-kubelet] with prefix node-csr",
			node:        "node-csr-cast-pool-1",
			signer:      certv1.KubeAPIServerClientKubeletSignerName,
			usages:      []certv1.KeyUsage{certv1.UsageClientAuth},
			username:    "kubelet-bootstrap",
		},
		{
			description: "[client-kubelet] with prefix csr",
			node:        "csr-cast-pool-2",
			signer:      certv1.KubeAPIServerClientKubeletSignerName,
			usages:      []certv1.KeyUsage{certv1.UsageClientAuth},
			username:    "kubelet-bootstrap",
		},
		{
			description: "[client-kubelet] unknown prefix",
			node:        "unknown-cast-pool-3",
			notApproved: true,
			signer:      certv1.KubeAPIServerClientKubeletSignerName,
			usages:      []certv1.KeyUsage{certv1.UsageClientAuth},
			username:    "kubelet-bootstrap",
		},
		{
			description: "[client-kubelet] with username kubelet-bootstrap",
			node:        "csr-cast-pool-4",
			signer:      certv1.KubeAPIServerClientKubeletSignerName,
			usages:      []certv1.KeyUsage{certv1.UsageClientAuth},
			username:    "kubelet-bootstrap",
		},
		{
			description: "[client-kubelet] with username serviceaccount",
			node:        "csr-cast-pool-5",
			signer:      certv1.KubeAPIServerClientKubeletSignerName,
			usages:      []certv1.KeyUsage{certv1.UsageClientAuth},
			username:    "system:serviceaccount:castai-agent:castai-cluster-controller",
		},
		{
			description: "[client-kubelet] with username prefix sytem:node",
			node:        "csr-cast-pool-6",
			signer:      certv1.KubeAPIServerClientKubeletSignerName,
			usages:      []certv1.KeyUsage{certv1.UsageClientAuth},
			username:    "system:node:some-text",
		},
		{
			description: "[client-kubelet] with unknown username",
			node:        "csr-cast-pool-7",
			notApproved: true,
			signer:      certv1.KubeAPIServerClientKubeletSignerName,
			usages:      []certv1.KeyUsage{certv1.UsageClientAuth},
			username:    "unknown-username-text",
		},
		{
			description: "[client-kubelet] with all allowed key usages",
			node:        "csr-cast-pool-8",
			signer:      certv1.KubeAPIServerClientKubeletSignerName,
			usages:      []certv1.KeyUsage{certv1.UsageClientAuth, certv1.UsageDigitalSignature, certv1.UsageKeyEncipherment},
			username:    "kubelet-bootstrap",
		},
		// TODO(furkhat@cast.ai)
		// {
		// 	description: "with not allowed key usages [" + certv1.KubeAPIServerClientKubeletSignerName + "]",
		// 	node:        "csr-cast-pool-9",
		// 	notApproved: true,
		// 	signer:      certv1.KubeAPIServerClientKubeletSignerName,
		// 	usages:      []certv1.KeyUsage{certv1.UsageClientAuth, certv1.UsageServerAuth},
		// 	username:    "kubelet-bootstrap",
		// },
		// TODO(furkhat@cast.ai): const system:nodes and system:node:
		{
			description: "[kubelet-serving] with prefix node-csr",
			groups:      []string{"system:nodes"},
			node:        "node-csr-cast-pool-10",
			signer:      certv1.KubeletServingSignerName,
			usages:      []certv1.KeyUsage{certv1.UsageServerAuth},
			username:    "system:node:node-csr-cast-pool-10",
		},
		{
			description: "[kubelet-serving] with prefix csr",
			groups:      []string{"system:nodes"},
			node:        "csr-cast-pool-11",
			signer:      certv1.KubeletServingSignerName,
			usages:      []certv1.KeyUsage{certv1.UsageServerAuth},
			username:    "system:node:csr-cast-pool-11",
		},
		{
			description: "[kubelet-serving] unknown prefix",
			groups:      []string{"system:nodes"},
			node:        "unknown-cast-pool-12",
			notApproved: true,
			signer:      certv1.KubeletServingSignerName,
			usages:      []certv1.KeyUsage{certv1.UsageServerAuth},
			username:    "system:node:unknown-cast-pool-12",
		},
		{
			description: "[kubelet-serving] with unknown username",
			groups:      []string{"system:nodes"},
			node:        "csr-cast-pool-13",
			notApproved: true,
			signer:      certv1.KubeletServingSignerName,
			usages:      []certv1.KeyUsage{certv1.UsageServerAuth},
			username:    "unknown-username-text",
		},
		{
			description: "[kubelet-serving] with groups system:nodes",
			groups:      []string{"system:nodes"},
			node:        "csr-cast-pool-13a",
			signer:      certv1.KubeletServingSignerName,
			usages:      []certv1.KeyUsage{certv1.UsageServerAuth},
			username:    "system:node:csr-cast-pool-13a",
		},
		{
			description: "[kubelet-serving] without required groups system:nodes",
			groups:      []string{"unknown-group"},
			node:        "csr-cast-pool-13b",
			notApproved: true,
			signer:      certv1.KubeletServingSignerName,
			usages:      []certv1.KeyUsage{certv1.UsageServerAuth},
			username:    "system:node:csr-cast-pool-13b",
		},
		{
			description: "[kubelet-serving] with all allowed key usages",
			groups:      []string{"system:nodes"},
			node:        "csr-cast-pool-14",
			signer:      certv1.KubeletServingSignerName,
			usages:      []certv1.KeyUsage{certv1.UsageServerAuth, certv1.UsageDigitalSignature, certv1.UsageKeyEncipherment},
			username:    "system:node:csr-cast-pool-14",
		},
		{
			description: "[kubelet-serving] without required server auth key usage",
			groups:      []string{"system:nodes"},
			node:        "csr-cast-pool-15",
			notApproved: true,
			signer:      certv1.KubeletServingSignerName,
			usages:      []certv1.KeyUsage{certv1.UsageClientAuth},
			username:    "system:node:csr-cast-pool-15",
		},
		{
			description: "[kubelet-serving] with not allowed key usages",
			groups:      []string{"system:nodes"},
			node:        "csr-cast-pool-16",
			notApproved: true,
			signer:      certv1.KubeletServingSignerName,
			usages:      []certv1.KeyUsage{certv1.UsageClientAuth, certv1.UsageServerAuth},
			username:    "system:node:csr-cast-pool-16",
		},
		{
			description: "[kubelet-serving] without email",
			groups:      []string{"system:nodes"},
			node:        "csr-cast-pool-17",
			signer:      certv1.KubeletServingSignerName,
			usages:      []certv1.KeyUsage{certv1.UsageServerAuth},
			username:    "system:node:csr-cast-pool-17",
		},
		{
			description: "[kubelet-serving] with emails",
			emails:      []string{"csr-cast-pool-18@some.org"},
			groups:      []string{"system:nodes"},
			node:        "csr-cast-pool-18",
			notApproved: true,
			signer:      certv1.KubeletServingSignerName,
			usages:      []certv1.KeyUsage{certv1.UsageServerAuth},
			username:    "system:node:csr-cast-pool-18",
		},
		{
			description: "[kubelet-serving] without uris",
			groups:      []string{"system:nodes"},
			node:        "csr-cast-pool-19",
			signer:      certv1.KubeletServingSignerName,
			usages:      []certv1.KeyUsage{certv1.UsageServerAuth},
			username:    "system:node:csr-cast-pool-19",
		},
		{
			description: "[kubelet-serving] with uris",
			groups:      []string{"system:nodes"},
			node:        "csr-cast-pool-20",
			notApproved: true,
			signer:      certv1.KubeletServingSignerName,
			uris:        []*url.URL{&url.URL{Path: "https://example.com"}},
			usages:      []certv1.KeyUsage{certv1.UsageServerAuth},
			username:    "system:node:csr-cast-pool-20",
		},
	} {
		t.Run(testcase.description, func(t *testing.T) {
			t.Parallel()
			if testcase.creationTimestamp.IsZero() {
				testcase.creationTimestamp = metav1.Now()
			}
			csr := &certv1.CertificateSigningRequest{
				ObjectMeta: metav1.ObjectMeta{
					Name:              testcase.node,
					CreationTimestamp: testcase.creationTimestamp,
				},
				Spec: certv1.CertificateSigningRequestSpec{
					Request: csrtest.NewEncodedCertificateRequest(t, &x509.CertificateRequest{
						Subject: pkix.Name{
							CommonName: "system:node:" + testcase.node,
						},
						EmailAddresses: testcase.emails,
						URIs:           testcase.uris,
					}),
					SignerName: testcase.signer,
					Username:   testcase.username,
					Usages:     testcase.usages,
					Groups:     testcase.groups,
				},
			}
			_, err := clientset.CertificatesV1().CertificateSigningRequests().Create(ctx, csr, metav1.CreateOptions{})
			r.NoError(err, "failed to create CSR")
			time.Sleep(10 * time.Millisecond)
			csr, err = clientset.CertificatesV1().CertificateSigningRequests().Get(ctx, csr.Name, metav1.GetOptions{})
			r.NoError(err, "failed to get CSR")
			approved := approvedCSR(csr)
			if testcase.notApproved {
				r.False(approved, "CSR should not be approved")
			} else {
				r.True(approved, "CSR should be approved")
			}
		})
	}
}

func approvedCSR(csr *certv1.CertificateSigningRequest) bool {
	for _, condition := range csr.Status.Conditions {
		if condition.Type == certv1.CertificateApproved {
			return true
		}
	}
	return false
}

func newManagerAndClientset(t *testing.T) (*csr.ApprovalManager, *fake.Clientset) {
	t.Helper()

	// Coppied and adapter https://github.com/kubernetes/client-go/blob/master/examples/fake-client
	watcherStarted := make(chan struct{})
	defer close(watcherStarted)
	// Create the fake client.
	client := fake.NewClientset()
	// A catch-all watch reactor that allows us to inject the watcherStarted channel.
	client.PrependWatchReactor("*", func(action clienttesting.Action) (handled bool, ret watch.Interface, err error) {
		gvr := action.GetResource()
		ns := action.GetNamespace()
		watch, err := client.Tracker().Watch(gvr, ns)
		if err != nil {
			return false, nil, err
		}
		watcherStarted <- struct{}{}
		return true, watch, nil
	})

	logger := logrus.New()
	logger.SetOutput(io.Discard)
	manager := csr.NewApprovalManager(logger, client)
	manager.Start(context.TODO())

	// The fake client doesn't support resource version. Any writes to the client
	// after the informer's initial LIST and before the informer establishing the
	// watcher will be missed by the informer. Therefore we wait until the watcher
	// starts.
	// Note that the fake client isn't designed to work with informer. It
	// doesn't support resource version. It's encouraged to use a real client
	// in an integration/E2E test if you need to test complex behavior with
	// informer/controllers.
	<-watcherStarted
	<-watcherStarted
	return manager, client
}

func alphanumeric(n int) string {
	nums := make([]byte, n)
	rand.Read(nums)
	alphanumerals := []byte("0123456789abcdefghijklmnoprstuvwxyz")
	var result []byte
	for _, v := range nums {
		result = append(result, alphanumerals[int(v)%len(alphanumerals)])
	}
	return string(result)
}
