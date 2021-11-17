package log

import (
	"context"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v4"
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

func NewExporter(logger *logrus.Logger, client castai.Client) Exporter {
	return &exporter{
		logger: logger,
		client: client,
		wg:     sync.WaitGroup{},
	}
}

type exporter struct {
	logger *logrus.Logger
	client castai.Client
	wg     sync.WaitGroup
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

	b := backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), 3), ctx)
	err := backoff.Retry(func() error {
		return e.client.SendLogs(ctx, req)
	}, b)

	if err != nil {
		e.logger.Debugf("sending logs: %v", err)
	}
}
