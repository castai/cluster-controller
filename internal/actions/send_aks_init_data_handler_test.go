package actions

import (
	"context"
	"github.com/google/uuid"
	"testing"

	"github.com/castai/cluster-controller/internal/castai"
	mock_castai "github.com/castai/cluster-controller/internal/castai/mock"
	"github.com/golang/mock/gomock"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestAKSInitDataHandler(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)

	m := gomock.NewController(t)
	client := mock_castai.NewMockCastAIClient(m)
	client.EXPECT().SendAKSInitData(gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, req *castai.AKSInitDataRequest) error {
			require.NotEmpty(t, req.CloudConfigBase64)
			require.NotEmpty(t, req.ProtectedSettingsBase64)
			return nil
		})
	h := SendAKSInitDataHandler{
		log:             log,
		client:          client,
		cloudConfigPath: "../testdata/aks/ovf-env.xml",
		baseDir:         "../testdata/aks",
	}

	action := &castai.ClusterAction{
		ID:                    uuid.New().String(),
		ActionSendAKSInitData: &castai.ActionSendAKSInitData{},
	}
	ctx := context.Background()
	err := h.Handle(ctx, action)
	require.NoError(t, err)
}
