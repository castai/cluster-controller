package csr

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	certv1 "k8s.io/api/certificates/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes/fake"
	ktest "k8s.io/client-go/testing"
)

func getCSR(name, username string) *certv1.CertificateSigningRequest {
	return &certv1.CertificateSigningRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			CreationTimestamp: metav1.Now(),
		},
		Spec: certv1.CertificateSigningRequestSpec{
			Request: []byte(`-----BEGIN CERTIFICATE REQUEST-----
MIIBLTCB0wIBADBPMRUwEwYDVQQKEwxzeXN0ZW06bm9kZXMxNjA0BgNVBAMTLXN5
c3RlbTpub2RlOmdrZS1kZXYtbWFzdGVyLWNhc3QtcG9vbC1jYjUzMTc3YjBZMBMG
ByqGSM49AgEGCCqGSM49AwEHA0IABMZKNQROiVpxfH4nHaPnE6NaY9Mr8/HBnxCl
mPe4mrvNGRnlJV+LvYCUAVlfinzLcMJSmRjJADgzN0Pn+i+4ra6gIjAgBgkqhkiG
9w0BCQ4xEzARMA8GA1UdEQQIMAaHBAoKADIwCgYIKoZIzj0EAwIDSQAwRgIhAOKQ
S59zc2bEaJ3y4aSMXLY3gmri14jZvvnFrxaPDT2PAiEA7C3hvZwrCJsoO61JWKqc
1ElMb/fzAVBcP34rfsE7qmQ=
-----END CERTIFICATE REQUEST-----`),
			SignerName: certv1.KubeAPIServerClientKubeletSignerName,
			Usages:     []certv1.KeyUsage{"kubelet"},
			Username:   username,
		},
		// Status: certv1.CertificateSigningRequestStatus{},.
	}
}

func TestCSRApprove(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)

	t.Run("approve v1 csr successfully", func(t *testing.T) {
		r := require.New(t)
		t.Parallel()

		csrName := "node-csr-123"
		userName := "kubelet-bootstrap"
		client := fake.NewClientset(getCSR(csrName, userName))
		s := NewApprovalManager(log, client)
		watcher := watch.NewFake()
		client.PrependWatchReactor("certificatesigningrequests", ktest.DefaultWatchReactor(watcher, nil))

		ctx := context.Background()
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			s.Start(ctx)
		}()
		go func() {
			defer wg.Done()
			watcher.Add(getCSR(csrName, userName))
			time.Sleep(100 * time.Millisecond)
			s.Stop()
		}()

		wg.Wait()

		csrResult, err := client.CertificatesV1().CertificateSigningRequests().Get(ctx, csrName, metav1.GetOptions{})
		r.NoError(err)

		r.Equal(csrResult.Status.Conditions[0].Type, certv1.CertificateApproved)
	})

	t.Run("not node csr do nothing", func(t *testing.T) {
		r := require.New(t)
		t.Parallel()

		csrName := "123"
		userName := "kubelet-bootstrap"
		client := fake.NewClientset(getCSR(csrName, userName))
		s := NewApprovalManager(log, client)
		watcher := watch.NewFake()
		client.PrependWatchReactor("certificatesigningrequests", ktest.DefaultWatchReactor(watcher, nil))

		ctx := context.Background()
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			s.Start(ctx)
		}()
		go func() {
			defer wg.Done()
			watcher.Add(getCSR(csrName, userName))
			time.Sleep(100 * time.Millisecond)
			s.Stop()
		}()

		wg.Wait()

		csrResult, err := client.CertificatesV1().CertificateSigningRequests().Get(ctx, csrName, metav1.GetOptions{})
		r.NoError(err)
		r.Len(csrResult.Status.Conditions, 0)
	})
}
