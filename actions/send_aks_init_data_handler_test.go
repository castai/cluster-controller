package actions

import (
	"context"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	mock_actions "github.com/castai/cluster-controller/actions/mock"
	"github.com/castai/cluster-controller/types"
)

func TestAKSInitDataHandler(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)

	mockCtrl := gomock.NewController(t)
	client := mock_actions.NewMockClient(mockCtrl)
	client.EXPECT().SendAKSInitData(gomock.Any(), gomock.Not(gomock.Len(0)), gomock.Not(gomock.Len(0)), gomock.Any()).Return(nil)

	h := sendAKSInitDataHandler{
		log:             log,
		client:          client,
		cloudConfigPath: "../testdata/aks/ovf-env.xml",
		baseDir:         "../testdata/aks",
	}
	ctx := context.Background()
	err := h.Handle(ctx, &types.ClusterAction{
		ID:                    uuid.New().String(),
		ActionSendAKSInitData: &types.ActionSendAKSInitData{},
	})
	if err != nil {
		t.Fatal(err)
	}
}
