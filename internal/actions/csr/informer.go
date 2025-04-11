package csr

import (
	"context"
	"fmt"
	"time"

	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

const (
	// We should approve CSRs, when they are created, so resync can be high.
	// Resync plays back all events (create, update, delete), which are in informer cache.
	// This does not involve talking to API server, it is not relist.
	csrInformerResyncPeriod = 12 * time.Hour
)

func startInformers(ctx context.Context, log logrus.FieldLogger, factories ...informers.SharedInformerFactory) {
	stopCh := make(chan struct{})
	defer close(stopCh)

	for _, factory := range factories {
		factory.Start(stopCh)
	}

	log.Info("watching for new node CSRs")

	<-ctx.Done()
	log.WithField("context", ctx.Err()).Info("finished watching for new node CSRs")
}

func createInformer(ctx context.Context, client kubernetes.Interface, fieldSelectorV1, fieldSelectorV1beta1 string) (informers.SharedInformerFactory, cache.SharedIndexInformer, error) {
	var (
		errv1      error
		errv1beta1 error
	)

	if _, errv1 = client.CertificatesV1().CertificateSigningRequests().List(ctx, metav1.ListOptions{}); errv1 == nil {
		v1Factory := informers.NewSharedInformerFactoryWithOptions(client, csrInformerResyncPeriod,
			informers.WithTweakListOptions(func(opts *metav1.ListOptions) {
				opts.FieldSelector = fieldSelectorV1
			}))
		v1Informer := v1Factory.Certificates().V1().CertificateSigningRequests().Informer()
		return v1Factory, v1Informer, nil
	}

	if _, errv1beta1 = client.CertificatesV1beta1().CertificateSigningRequests().List(ctx, metav1.ListOptions{}); errv1beta1 == nil {
		v1Factory := informers.NewSharedInformerFactoryWithOptions(client, csrInformerResyncPeriod,
			informers.WithTweakListOptions(func(opts *metav1.ListOptions) {
				opts.FieldSelector = fieldSelectorV1beta1
			}))
		v1Informer := v1Factory.Certificates().V1beta1().CertificateSigningRequests().Informer()
		return v1Factory, v1Informer, nil
	}

	return nil, nil, fmt.Errorf("failed to create informer: v1: %w, v1beta1: %w", errv1, errv1beta1)
}

//nolint:unparam
func listOptionsWithSigner(signer string) metav1.ListOptions {
	return metav1.ListOptions{
		FieldSelector: fields.SelectorFromSet(fields.Set{
			"spec.signerName": signer,
		}).String(),
	}
}
