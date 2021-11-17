package log

import (
	"context"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/castai/cluster-controller/castai"
)

const (
	sendTimeout = 15 * time.Second
)

type Exporter interface {
	logrus.Hook
	Wait()
}

func SetupExporter(logger *logrus.Logger, client castai.Client) {
	e := &exporter{
		client: client,
		wg:     sync.WaitGroup{},
	}

	logger.AddHook(e)
	logrus.RegisterExitHandler(e.Wait)
}

type exporter struct {
	client    castai.Client
	wg        sync.WaitGroup
	clusterID string
}

func (e *exporter) Levels() []logrus.Level {
	return []logrus.Level{
		logrus.ErrorLevel,
		logrus.FatalLevel,
		logrus.PanicLevel,
		logrus.InfoLevel,
		logrus.WarnLevel,
	}
}

func (e *exporter) Fire(entry *logrus.Entry) error {
	e.wg.Add(1)

	go func(entry *logrus.Entry) {
		defer e.wg.Done()
		e.sendLogEvent(entry)
	}(entry)

	return nil
}

func (e *exporter) Wait() {
	e.wg.Wait()
}

func (e *exporter) sendLogEvent(log *logrus.Entry) {
	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()

	req := &castai.LogEvent{
		Level:   log.Level.String(),
		Time:    log.Time,
		Message: log.Message,
		Fields:  log.Data,
	}

	_ = e.client.SendLogs(ctx, req)
}
