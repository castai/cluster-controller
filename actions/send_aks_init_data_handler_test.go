package actions

import (
	"context"
	"testing"

	"github.com/castai/cluster-controller/actions/types"
	"github.com/google/uuid"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestAKSInitDataHandler(t *testing.T) {
	r := require.New(t)
	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)

	client := newMockAPIClient(nil)
	h := sendAKSInitDataHandler{
		log:             log,
		client:          client,
		cloudConfigPath: "../testdata/aks/ovf-env.xml",
		baseDir:         "../testdata/aks",
	}

	action := &types.ClusterAction{
		ID:                    uuid.New().String(),
		ActionSendAKSInitData: &types.ActionSendAKSInitData{},
	}
	ctx := context.Background()
	err := h.Handle(ctx, action)

	r.NoError(err)
	r.NotEmpty(client.AKSInitDataReq.CloudConfigBase64)
	r.NotEmpty(client.AKSInitDataReq.ProtectedSettingsBase64)
}
