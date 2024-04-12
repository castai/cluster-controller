package logexporter

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
	it := require.New(t)
	logger, hook := test.NewNullLogger()
	defer hook.Reset()

	sender := &mockSender{}
	e := New(nil, sender)
	logger.AddHook(e)
	log := logger.WithFields(logrus.Fields{
		"cluster_id": "test-cluster",
	})

	log.Log(logrus.ErrorLevel, "failed to add node")
	log.Log(logrus.DebugLevel, "sending logs")
	e.Wait()

	it.Len(sender.Entries, 1)
	it.Equal(sender.Entries[0].Message, "failed to add node")
	it.Equal(sender.Entries[0].Level, "error")
	it.Equal(sender.Entries[0].Fields, logrus.Fields{"cluster_id": "test-cluster"})
}

type mockSender struct {
	mu      sync.Mutex
	Entries []*Entry
}

func (s *mockSender) SendLog(_ context.Context, entry *Entry) error {
	s.mu.Lock()
	s.Entries = append(s.Entries, entry)
	s.mu.Unlock()
	return nil
}
