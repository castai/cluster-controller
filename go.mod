module github.com/castai/cluster-controller

go 1.16

require (
	github.com/cenkalti/backoff/v4 v4.1.1
	github.com/go-resty/resty/v2 v2.5.0
	github.com/golang/mock v1.6.0
	github.com/google/uuid v1.1.2
	github.com/sirupsen/logrus v1.7.0
	github.com/spf13/viper v1.7.1
	github.com/stretchr/testify v1.7.0
	go.uber.org/goleak v1.1.12
	golang.org/x/sync v0.0.0-20210220032951-036812b2e83c
	k8s.io/api v0.22.1
	k8s.io/apimachinery v0.22.1
	k8s.io/client-go v0.22.1
	sigs.k8s.io/controller-runtime v0.8.3
)
