package log

import (
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/castai/cluster-controller/castai/mock"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func TestLogExporter(t *testing.T) {
	it := require.New(t)
	logger, hook := test.NewNullLogger()
	defer hook.Reset()

	client := mock.NewMockAPIClient(nil)
	e := NewExporter(nil, client)
	logger.AddHook(e)
	log := logger.WithFields(logrus.Fields{
		"cluster_id": "test-cluster",
	})

	log.Log(logrus.ErrorLevel, "failed to add node")
	log.Log(logrus.DebugLevel, "sending logs")
	e.Wait()

	it.Len(client.Logs, 1)
	it.Equal(client.Logs[0].Message, "failed to add node")
	it.Equal(client.Logs[0].Level, "error")
	it.Equal(client.Logs[0].Fields, logrus.Fields{"cluster_id": "test-cluster"})
}
