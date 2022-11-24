package aks

import (
	"context"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/castai/cluster-controller/castai/mock"
)

func TestAKSInitDataHandler(t *testing.T) {
	r := require.New(t)
	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)

	client := mock.NewMockAPIClient(nil)
	h := InitDataHandler{
		log:             log,
		client:          client,
		cloudConfigPath: "../testdata/aks/ovf-env.xml",
		baseDir:         "../testdata/aks",
	}

	ctx := context.Background()
	err := h.Handle(ctx)

	r.NoError(err)
	r.NotEmpty(client.AKSInitDataReq.CloudConfigBase64)
	r.NotEmpty(client.AKSInitDataReq.ProtectedSettingsBase64)
	r.NotEmpty(client.AKSInitDataReq.Architecture)
}
