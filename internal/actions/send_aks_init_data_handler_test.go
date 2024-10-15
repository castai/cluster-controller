package actions

import (
	"context"
	"github.com/google/uuid"
	"testing"

	mock_castai "github.com/castai/cluster-controller/internal/castai/mock"
	"github.com/castai/cluster-controller/internal/types"
	"github.com/golang/mock/gomock"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestAKSInitDataHandler(t *testing.T) {
	r := require.New(t)
	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)

	m := gomock.NewController(t)
	client := mock_castai.NewMockCastAIClient(m)
	h := SendAKSInitDataHandler{
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
	//r.NotEmpty(client.AKSInitDataReq.CloudConfigBase64)
	//r.NotEmpty(client.AKSInitDataReq.ProtectedSettingsBase64)
}
