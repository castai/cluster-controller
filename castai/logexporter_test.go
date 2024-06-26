package castai

import (
	"context"
	"sync"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m, goleak.IgnoreTopFunction("k8s.io/klog/v2.(*loggingT).flushDaemon"))
}

func TestLogExporter(t *testing.T) {
	logger, hook := test.NewNullLogger()
	defer hook.Reset()

	sender := &mockSender{}
	e := NewLogExporter(nil, sender)
	logger.AddHook(e)
	log := logger.WithFields(logrus.Fields{
		"cluster_id": "test-cluster",
	})
	log.Log(logrus.ErrorLevel, "foo")
	log.Log(logrus.DebugLevel, "bar")
	e.Wait()

	it := require.New(t)
	it.Len(sender.entries, 1)
	it.Equal(sender.entries[0].Message, "foo")
	it.Equal(sender.entries[0].Level, "error")
	it.Equal(sender.entries[0].Fields, logrus.Fields{"cluster_id": "test-cluster"})
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
