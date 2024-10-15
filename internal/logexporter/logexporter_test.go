package logexporter_test

import (
	"testing"

	"fmt"
	"github.com/castai/cluster-controller/internal/logexporter"
	mock_logexporter "github.com/castai/cluster-controller/internal/logexporter/mock"
	"github.com/golang/mock/gomock"
	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m, goleak.IgnoreTopFunction("k8s.io/klog/v2.(*loggingT).flushDaemon"))
}

func TestSetupLogExporter(t *testing.T) {
	t.Parallel()
	type args struct {
		tuneMockSender func(sender *mock_logexporter.MockLogSender)
		msg            map[int]string // level -> message
	}
	tests := []struct {
		name string
		args args
	}{
		{
			name: "1 error, 1 debug",
			args: args{
				msg: map[int]string{
					int(logrus.ErrorLevel): "foo",
					int(logrus.DebugLevel): "bar",
				},
				tuneMockSender: func(sender *mock_logexporter.MockLogSender) {
					sender.EXPECT().SendLog(gomock.Any(), gomock.Any()).
						Return(nil).Times(1)
				},
			},
		},
		{
			name: "sendLog error",
			args: args{
				msg: map[int]string{
					int(logrus.ErrorLevel): "foo",
					int(logrus.DebugLevel): "bar",
				},
				tuneMockSender: func(sender *mock_logexporter.MockLogSender) {
					sender.EXPECT().SendLog(gomock.Any(), gomock.Any()).
						Return(fmt.Errorf("test-error")).Times(4)
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
			sender := mock_logexporter.NewMockLogSender(m)
			if tt.args.tuneMockSender != nil {
				tt.args.tuneMockSender(sender)
			}
			logger, hook := test.NewNullLogger()
			defer hook.Reset()

			e := logexporter.NewLogExporter(logger, sender)
			logger.AddHook(e)
			log := logger.WithFields(logrus.Fields{
				"cluster_id": "test-cluster",
			})
			for level, msg := range tt.args.msg {
				log.Log(logrus.Level(level), msg)
			}
			e.Wait()

		})
	}
}
