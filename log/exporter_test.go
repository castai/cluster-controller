package log

import (
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/require"

	"github.com/castai/cluster-controller/castai/mock"
)

func TestSetupLogExporter(t *testing.T) {
	it := require.New(t)
	logger, hook := test.NewNullLogger()
	defer hook.Reset()

	client := mock.NewMockAPIClient(nil)
	e := NewExporter(client)
	logger.AddHook(e)
	log := logger.WithFields(logrus.Fields{
		"cluster_id": "test-cluster",
	})

	log.Log(logrus.InfoLevel, "deleting kubernetes node")
	e.Wait()

	it.Len(client.Logs, 1)
	it.Equal(client.Logs[0].Message, "deleting kubernetes node")
	it.Equal(client.Logs[0].Level, "info")
	it.Equal(client.Logs[0].Fields, logrus.Fields{"cluster_id": "test-cluster"})
}
