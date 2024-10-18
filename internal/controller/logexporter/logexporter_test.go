package logexporter

import (
	"fmt"
	"testing"

	mock_castai "github.com/castai/cluster-controller/internal/castai/mock"
	"github.com/golang/mock/gomock"
	"github.com/sirupsen/logrus"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m, goleak.IgnoreTopFunction("k8s.io/klog/v2.(*loggingT).flushDaemon"))
}

func TestSetupLogExporter(t *testing.T) {
	t.Parallel()
	type args struct {
		tuneMockSender func(sender *mock_castai.MockCastAIClient)
		msg            map[uint32]string // level -> message
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "1 error, 1 debug",
			args: args{
				msg: map[uint32]string{
					uint32(logrus.ErrorLevel): "foo",
					uint32(logrus.DebugLevel): "bar",
				},
				tuneMockSender: func(sender *mock_castai.MockCastAIClient) {
					sender.EXPECT().SendLog(gomock.Any(), gomock.Any()).
						Return(nil).Times(1)
				},
			},
		},
		{
			name: "sendLog error",
			args: args{
				msg: map[uint32]string{
					uint32(logrus.ErrorLevel): "foo",
					uint32(logrus.DebugLevel): "bar",
				},
				tuneMockSender: func(sender *mock_castai.MockCastAIClient) {
					sender.EXPECT().SendLog(gomock.Any(), gomock.Any()).
						Return(fmt.Errorf("test-error")).Times(4) // 1 for first error, 3 for retries
				},
			},
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := gomock.NewController(t)
			defer m.Finish()
			sender := mock_castai.NewMockCastAIClient(m)
			if tt.args.tuneMockSender != nil {
				tt.args.tuneMockSender(sender)
			}
			logger := NewLogger(uint32(logrus.InfoLevel))

			logExporter := newLogExporter(logger, sender)
			logger.AddHook(logExporter)
			defer logExporter.Wait()

			log := logger.WithFields(logrus.Fields{
				"cluster_id": "test-cluster",
			})
			for level, msg := range tt.args.msg {
				log.Log(logrus.Level(level), msg)
			}
		})
	}
}
