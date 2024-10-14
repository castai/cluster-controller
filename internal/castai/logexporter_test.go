package castai

import (
	"testing"

	"context"
	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
	"sync"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m, goleak.IgnoreTopFunction("k8s.io/klog/v2.(*loggingT).flushDaemon"))
}

func TestLogExporter(t *testing.T) {
	logger, hook := test.NewNullLogger()
	defer hook.Reset()

	sender := &mockSender{}
	SetupLogExporter(logger, sender)
	log := logger.WithFields(logrus.Fields{
		"cluster_id": "test-cluster",
	})
	log.Log(logrus.ErrorLevel, "foo")
	log.Log(logrus.DebugLevel, "bar")

	it := require.New(t)
	it.Equal(sender.Len(), 1)
	it.Equal("foo", sender.entries[0].Message)
	it.Equal("error", sender.entries[0].Level)
	it.Equal(logrus.Fields{"cluster_id": "test-cluster"}, sender.entries[0].Fields)
}

type mockSender struct {
	mu      sync.Mutex
	entries []*logEntry
}

func (s *mockSender) SendLog(_ context.Context, entry *logEntry) error {
	s.mu.Lock()
	s.entries = append(s.entries, entry)
	s.mu.Unlock()
	return nil
}
func (s *mockSender) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.entries)
}
