package csr_test

import (
	"context"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"net"
	"net/url"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	certv1 "k8s.io/api/certificates/v1"
	certv1beta1 "k8s.io/api/certificates/v1beta1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/castai/cluster-controller/internal/actions/csr"
	csrtest "github.com/castai/cluster-controller/internal/actions/csr/test"
)

func TestIntegrationV1(t *testing.T) {
	t.Parallel()
	testIntegration(t, certv1.SchemeGroupVersion)
}

func TestIntegrationV1beta1(t *testing.T) {
	t.Parallel()
	testIntegration(t, certv1beta1.SchemeGroupVersion)
}

func testIntegration(t *testing.T, csrVersion schema.GroupVersion) {
	ctx := context.TODO()
	r := require.New(t)
	clientset := setupManagerAndClientset(t, csrVersion)
	for _, testcase := range []struct {
		creationTimestamp     metav1.Time
		description           string
		emails                []string
		groups                []string
		nodeCreatedWithStatus *corev1.NodeStatus
		nodeName              string
		notApproved           bool
		signer                string
		uris                  []*url.URL
		usages                []string
		username              string
		ips                   []net.IP
		dns                   []string
	}{
		{
			description:       "[client-kubelet] outdated",
			nodeName:          "node-csr-cast-pool-0",
			signer:            certv1.KubeAPIServerClientKubeletSignerName,
			usages:            []string{string(certv1.UsageClientAuth)},
			username:          "kubelet-bootstrap",
			creationTimestamp: metav1.NewTime(time.Now().Add(-time.Hour - 1*time.Minute)),
			notApproved:       true,
		},
		{
			description: "[client-kubelet] with prefix node-csr",
			nodeName:    "node-csr-cast-pool-1",
			signer:      certv1.KubeAPIServerClientKubeletSignerName,
			usages:      []string{string(certv1.UsageClientAuth)},
			username:    "kubelet-bootstrap",
		},
		{
			description: "[client-kubelet] with prefix csr",
			nodeName:    "csr-cast-pool-2",
			signer:      certv1.KubeAPIServerClientKubeletSignerName,
			usages:      []string{string(certv1.UsageClientAuth)},
			username:    "kubelet-bootstrap",
		},
		{
			description: "[client-kubelet] unknown prefix",
			nodeName:    "unknown-cast-pool-3",
			notApproved: true,
			signer:      certv1.KubeAPIServerClientKubeletSignerName,
			usages:      []string{string(certv1.UsageClientAuth)},
			username:    "kubelet-bootstrap",
		},
		{
			description: "[client-kubelet] without cast-pool in subject CN",
			nodeName:    "node-csr-some-text",
			notApproved: true,
			signer:      certv1.KubeAPIServerClientKubeletSignerName,
			usages:      []string{string(certv1.UsageClientAuth)},
			username:    "kubelet-bootstrap",
		},
		{
			description: "[client-kubelet] with username kubelet-bootstrap",
			nodeName:    "csr-cast-pool-4",
			signer:      certv1.KubeAPIServerClientKubeletSignerName,
			usages:      []string{string(certv1.UsageClientAuth)},
			username:    "kubelet-bootstrap",
		},
		{
			description: "[client-kubelet] with username serviceaccount",
			nodeName:    "csr-cast-pool-5",
			signer:      certv1.KubeAPIServerClientKubeletSignerName,
			usages:      []string{string(certv1.UsageClientAuth)},
			username:    "system:serviceaccount:castai-agent:castai-cluster-controller",
		},
		{
			description: "[client-kubelet] with username prefix sytem:node",
			nodeName:    "csr-cast-pool-6",
			signer:      certv1.KubeAPIServerClientKubeletSignerName,
			usages:      []string{string(certv1.UsageClientAuth)},
			username:    "system:node:some-text",
		},
		{
			description: "[client-kubelet] with unknown username",
			nodeName:    "csr-cast-pool-7",
			notApproved: true,
			signer:      certv1.KubeAPIServerClientKubeletSignerName,
			usages:      []string{string(certv1.UsageClientAuth)},
			username:    "unknown-username-text",
		},
		{
			description: "[client-kubelet] with all allowed key usages",
			nodeName:    "csr-cast-pool-8",
			signer:      certv1.KubeAPIServerClientKubeletSignerName,
			usages:      []string{string(certv1.UsageClientAuth), string(certv1.UsageDigitalSignature), string(certv1.UsageKeyEncipherment)},
			username:    "kubelet-bootstrap",
		},
		{
			description: "[client-kubelet] with not allowed key usages",
			nodeName:    "csr-cast-pool-9",
			notApproved: true,
			signer:      certv1.KubeAPIServerClientKubeletSignerName,
			usages:      []string{string(certv1.UsageClientAuth), string(certv1.UsageServerAuth)},
			username:    "kubelet-bootstrap",
		},
		{
			description:           "[kubelet-serving] with prefix node-csr",
			groups:                []string{"system:nodes"},
			nodeCreatedWithStatus: &corev1.NodeStatus{},
			nodeName:              "node-csr-cast-pool-10",
			signer:                certv1.KubeletServingSignerName,
			usages:                []string{string(certv1.UsageServerAuth)},
			username:              "system:node:node-csr-cast-pool-10",
		},
		{
			description: "[kubelet-serving] without matching node",
			groups:      []string{"system:nodes"},
			nodeName:    "node-csr-cast-pool-10a",
			notApproved: true,
			signer:      certv1.KubeletServingSignerName,
			usages:      []string{string(certv1.UsageServerAuth)},
			username:    "system:node:node-csr-cast-pool-10a",
		},
		{
			description: "[kubelet-serving] with matching Internal IP",
			groups:      []string{"system:nodes"},
			nodeCreatedWithStatus: &corev1.NodeStatus{
				Addresses: []corev1.NodeAddress{
					{
						Type:    corev1.NodeInternalIP,
						Address: "10.0.0.123",
					},
				},
			},
			nodeName: "node-csr-cast-pool-10b",
			signer:   certv1.KubeletServingSignerName,
			usages:   []string{string(certv1.UsageServerAuth)},
			username: "system:node:node-csr-cast-pool-10b",
			ips: []net.IP{
				net.IPv4(10, 0, 0, 123),
			},
		},
		{
			description: "[kubelet-serving] with mismatching Internal IP",
			groups:      []string{"system:nodes"},
			nodeCreatedWithStatus: &corev1.NodeStatus{
				Addresses: []corev1.NodeAddress{
					{
						Type:    corev1.NodeInternalIP,
						Address: "10.0.0.1",
					},
				},
			},
			nodeName:    "node-csr-cast-pool-10c",
			notApproved: true,
			signer:      certv1.KubeletServingSignerName,
			usages:      []string{string(certv1.UsageServerAuth)},
			username:    "system:node:node-csr-cast-pool-10c",
			ips: []net.IP{
				net.IPv4(10, 0, 0, 123),
			},
		},
		{
			description: "[kubelet-serving] with matching External IP",
			groups:      []string{"system:nodes"},
			nodeCreatedWithStatus: &corev1.NodeStatus{
				Addresses: []corev1.NodeAddress{
					{
						Type:    corev1.NodeExternalIP,
						Address: "10.0.0.123",
					},
				},
			},
			nodeName: "node-csr-cast-pool-10d",
			signer:   certv1.KubeletServingSignerName,
			usages:   []string{string(certv1.UsageServerAuth)},
			username: "system:node:node-csr-cast-pool-10d",
			ips: []net.IP{
				net.IPv4(10, 0, 0, 123),
			},
		},
		{
			description: "[kubelet-serving] with mismatching External IP",
			groups:      []string{"system:nodes"},
			nodeCreatedWithStatus: &corev1.NodeStatus{
				Addresses: []corev1.NodeAddress{
					{
						Type:    corev1.NodeExternalIP,
						Address: "10.0.0.1",
					},
				},
			},
			nodeName:    "node-csr-cast-pool-10e",
			notApproved: true,
			signer:      certv1.KubeletServingSignerName,
			usages:      []string{string(certv1.UsageServerAuth)},
			username:    "system:node:node-csr-cast-pool-10e",
			ips: []net.IP{
				net.IPv4(10, 0, 0, 123),
			},
		},
		{
			description: "[kubelet-serving] with matching Internal DNS",
			groups:      []string{"system:nodes"},
			nodeCreatedWithStatus: &corev1.NodeStatus{
				Addresses: []corev1.NodeAddress{
					{
						Type:    corev1.NodeInternalDNS,
						Address: "foo.bar",
					},
				},
			},
			nodeName: "node-csr-cast-pool-10f",
			signer:   certv1.KubeletServingSignerName,
			usages:   []string{string(certv1.UsageServerAuth)},
			username: "system:node:node-csr-cast-pool-10f",
			dns:      []string{"foo.bar"},
		},
		{
			description: "[kubelet-serving] with mismatching Internal DNS",
			groups:      []string{"system:nodes"},
			nodeCreatedWithStatus: &corev1.NodeStatus{
				Addresses: []corev1.NodeAddress{
					{
						Type:    corev1.NodeInternalDNS,
						Address: "some.text",
					},
				},
			},
			nodeName:    "node-csr-cast-pool-10g",
			notApproved: true,
			signer:      certv1.KubeletServingSignerName,
			usages:      []string{string(certv1.UsageServerAuth)},
			username:    "system:node:node-csr-cast-pool-10g",
			dns:         []string{"foo.bar"},
		},
		{
			description: "[kubelet-serving] with matching External DNS",
			groups:      []string{"system:nodes"},
			nodeCreatedWithStatus: &corev1.NodeStatus{
				Addresses: []corev1.NodeAddress{
					{
						Type:    corev1.NodeExternalDNS,
						Address: "foo.bar",
					},
				},
			},
			nodeName: "node-csr-cast-pool-10h",
			signer:   certv1.KubeletServingSignerName,
			usages:   []string{string(certv1.UsageServerAuth)},
			username: "system:node:node-csr-cast-pool-10h",
			dns:      []string{"foo.bar"},
		},
		{
			description: "[kubelet-serving] with mismatching External DNS",
			groups:      []string{"system:nodes"},
			nodeCreatedWithStatus: &corev1.NodeStatus{
				Addresses: []corev1.NodeAddress{
					{
						Type:    corev1.NodeExternalDNS,
						Address: "some.text",
					},
				},
			},
			nodeName:    "node-csr-cast-pool-10i",
			notApproved: true,
			signer:      certv1.KubeletServingSignerName,
			usages:      []string{string(certv1.UsageServerAuth)},
			username:    "system:node:node-csr-cast-pool-10i",
			dns:         []string{"foo.bar"},
		},
		{
			description:           "[kubelet-serving] with prefix csr",
			groups:                []string{"system:nodes"},
			nodeCreatedWithStatus: &corev1.NodeStatus{},
			nodeName:              "csr-cast-pool-11",
			signer:                certv1.KubeletServingSignerName,
			usages:                []string{string(certv1.UsageServerAuth)},
			username:              "system:node:csr-cast-pool-11",
		},
		{
			description:           "[kubelet-serving] unknown prefix",
			groups:                []string{"system:nodes"},
			nodeCreatedWithStatus: &corev1.NodeStatus{},
			nodeName:              "unknown-cast-pool-12",
			notApproved:           true,
			signer:                certv1.KubeletServingSignerName,
			usages:                []string{string(certv1.UsageServerAuth)},
			username:              "system:node:unknown-cast-pool-12",
		},
		{
			description:           "[kubelet-serving] with unknown username",
			groups:                []string{"system:nodes"},
			nodeCreatedWithStatus: &corev1.NodeStatus{},
			nodeName:              "csr-cast-pool-13",
			notApproved:           true,
			signer:                certv1.KubeletServingSignerName,
			usages:                []string{string(certv1.UsageServerAuth)},
			username:              "unknown-username-text",
		},
		{
			description:           "[kubelet-serving] with groups system:nodes",
			groups:                []string{"system:nodes"},
			nodeCreatedWithStatus: &corev1.NodeStatus{},
			nodeName:              "csr-cast-pool-13a",
			signer:                certv1.KubeletServingSignerName,
			usages:                []string{string(certv1.UsageServerAuth)},
			username:              "system:node:csr-cast-pool-13a",
		},
		{
			description:           "[kubelet-serving] without required groups system:nodes",
			groups:                []string{"unknown-group"},
			nodeCreatedWithStatus: &corev1.NodeStatus{},
			nodeName:              "csr-cast-pool-13b",
			notApproved:           true,
			signer:                certv1.KubeletServingSignerName,
			usages:                []string{string(certv1.UsageServerAuth)},
			username:              "system:node:csr-cast-pool-13b",
		},
		{
			description:           "[kubelet-serving] with all allowed key usages",
			groups:                []string{"system:nodes"},
			nodeCreatedWithStatus: &corev1.NodeStatus{},
			nodeName:              "csr-cast-pool-14",
			signer:                certv1.KubeletServingSignerName,
			usages:                []string{string(certv1.UsageServerAuth), string(certv1.UsageDigitalSignature), string(certv1.UsageKeyEncipherment)},
			username:              "system:node:csr-cast-pool-14",
		},
		{
			description:           "[kubelet-serving] without required server auth key usage",
			groups:                []string{"system:nodes"},
			nodeCreatedWithStatus: &corev1.NodeStatus{},
			nodeName:              "csr-cast-pool-15",
			notApproved:           true,
			signer:                certv1.KubeletServingSignerName,
			usages:                []string{string(certv1.UsageClientAuth)},
			username:              "system:node:csr-cast-pool-15",
		},
		{
			description:           "[kubelet-serving] with not allowed key usages",
			groups:                []string{"system:nodes"},
			nodeCreatedWithStatus: &corev1.NodeStatus{},
			nodeName:              "csr-cast-pool-16",
			notApproved:           true,
			signer:                certv1.KubeletServingSignerName,
			usages:                []string{string(certv1.UsageClientAuth), string(certv1.UsageServerAuth)},
			username:              "system:node:csr-cast-pool-16",
		},
		{
			description:           "[kubelet-serving] with emails",
			emails:                []string{"csr-cast-pool-18@some.org"},
			groups:                []string{"system:nodes"},
			nodeCreatedWithStatus: &corev1.NodeStatus{},
			nodeName:              "csr-cast-pool-18",
			notApproved:           true,
			signer:                certv1.KubeletServingSignerName,
			usages:                []string{string(certv1.UsageServerAuth)},
			username:              "system:node:csr-cast-pool-18",
		},
		{
			description:           "[kubelet-serving] with uris",
			groups:                []string{"system:nodes"},
			nodeCreatedWithStatus: &corev1.NodeStatus{},
			nodeName:              "csr-cast-pool-20",
			notApproved:           true,
			signer:                certv1.KubeletServingSignerName,
			uris:                  []*url.URL{{Path: "https://example.com"}},
			usages:                []string{string(certv1.UsageServerAuth)},
			username:              "system:node:csr-cast-pool-20",
		},
		{
			description:           "with unmanaged signer",
			nodeCreatedWithStatus: &corev1.NodeStatus{},
			nodeName:              "csr-cast-pool-21",
			notApproved:           true,
			signer:                certv1.KubeAPIServerClientKubeletSignerName,
			username:              "system:nodes:csr-cast-pool-21",
		},
	} {
		t.Run(csrVersion.Version+" "+testcase.description, func(t *testing.T) {
			if testcase.creationTimestamp.IsZero() {
				testcase.creationTimestamp = metav1.Now()
			}
			if testcase.nodeCreatedWithStatus != nil {
				node := &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name:              testcase.nodeName,
						CreationTimestamp: testcase.creationTimestamp,
					},
					Status: *testcase.nodeCreatedWithStatus,
				}
				_, err := clientset.CoreV1().Nodes().Create(ctx, node, metav1.CreateOptions{})
				r.NoError(err, "failed to create node")
			}
			if csrVersion == certv1.SchemeGroupVersion {
				csr := &certv1.CertificateSigningRequest{
					ObjectMeta: metav1.ObjectMeta{
						Name:              testcase.nodeName,
						CreationTimestamp: testcase.creationTimestamp,
					},
					Spec: certv1.CertificateSigningRequestSpec{
						Groups: testcase.groups,
						Request: csrtest.NewEncodedCertificateRequest(t, &x509.CertificateRequest{
							EmailAddresses: testcase.emails,
							IPAddresses:    testcase.ips,
							DNSNames:       testcase.dns,
							Subject: pkix.Name{
								CommonName: "system:node:" + testcase.nodeName,
							},
							URIs: testcase.uris,
						}),
						SignerName: testcase.signer,
						Usages: lo.Map(testcase.usages, func(u string, _ int) certv1.KeyUsage {
							return certv1.KeyUsage(u)
						}),
						Username: testcase.username,
					},
				}
				_, err := clientset.CertificatesV1().CertificateSigningRequests().Create(ctx, csr, metav1.CreateOptions{})
				r.NoError(err, "failed to create CSR")
				time.Sleep(10 * time.Millisecond)
				csr, err = clientset.CertificatesV1().CertificateSigningRequests().Get(ctx, csr.Name, metav1.GetOptions{})
				r.NoError(err, "failed to get CSR")
				approved := approvedCSRV1(csr)
				if testcase.notApproved {
					r.Falsef(approved, "%s - must not be approved", testcase.description)
				} else {
					r.Truef(approved, "%s - must be approved", testcase.description)
				}
			} else {
				csr := &certv1beta1.CertificateSigningRequest{
					ObjectMeta: metav1.ObjectMeta{
						Name:              testcase.nodeName,
						CreationTimestamp: testcase.creationTimestamp,
					},
					Spec: certv1beta1.CertificateSigningRequestSpec{
						Groups: testcase.groups,
						Request: csrtest.NewEncodedCertificateRequest(t, &x509.CertificateRequest{
							EmailAddresses: testcase.emails,
							IPAddresses:    testcase.ips,
							DNSNames:       testcase.dns,
							Subject: pkix.Name{
								CommonName: "system:node:" + testcase.nodeName,
							},
							URIs: testcase.uris,
						}),
						SignerName: lo.ToPtr(testcase.signer),
						Usages: lo.Map(testcase.usages, func(u string, _ int) certv1beta1.KeyUsage {
							return certv1beta1.KeyUsage(u)
						}),
						Username: testcase.username,
					},
				}
				_, err := clientset.CertificatesV1beta1().CertificateSigningRequests().Create(ctx, csr, metav1.CreateOptions{})
				r.NoError(err, "failed to create CSR")
				time.Sleep(10 * time.Millisecond)
				csr, err = clientset.CertificatesV1beta1().CertificateSigningRequests().Get(ctx, csr.Name, metav1.GetOptions{})
				r.NoError(err, "failed to get CSR")
				approved := approvedCSRV1beta1(csr)
				if testcase.notApproved {
					r.Falsef(approved, "%s - must not be approved", testcase.description)
				} else {
					r.Truef(approved, "%s - must be approved", testcase.description)
				}
			}
		})
	}
}

func approvedCSRV1(csr *certv1.CertificateSigningRequest) bool {
	for _, condition := range csr.Status.Conditions {
		if condition.Type == certv1.CertificateApproved && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func approvedCSRV1beta1(csr *certv1beta1.CertificateSigningRequest) bool {
	for _, condition := range csr.Status.Conditions {
		if condition.Type == certv1beta1.CertificateApproved && condition.Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}

func setupManagerAndClientset(t *testing.T, csrVersion schema.GroupVersion) *fake.Clientset {
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
	client.PrependReactor("*", "*", func(action clienttesting.Action) (handled bool, ret runtime.Object, err error) {
		if action.GetResource().Resource == "certificatesigningrequests" && action.GetResource().GroupVersion() != csrVersion {
			err = apierrors.NewNotFound(schema.GroupResource{}, action.GetResource().String())
			return true, nil, err
		}
		return
	})
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	manager := csr.NewApprovalManager(logger, client)
	err := manager.Start(context.TODO())
	require.NoError(t, err, "failed to start approval manager")

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
	return client
}
