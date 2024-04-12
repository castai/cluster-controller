package logexporter

import (
	"context"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/sirupsen/logrus"
)

const (
	sendTimeout = 15 * time.Second
)

type Exporter interface {
	logrus.Hook
	Wait()
}

type Sender interface {
	SendLog(ctx context.Context, entry *Entry) error
}

type Entry struct {
	Level   string        `json:"level"`
	Time    time.Time     `json:"time"`
	Message string        `json:"message"`
	Fields  logrus.Fields `json:"fields"`
}

type exporter struct {
	logger *logrus.Logger
	sender Sender
	wg     sync.WaitGroup
}

// exporter must satisfy logrus.Hook.
var _ logrus.Hook = new(exporter)

func New(logger *logrus.Logger, sender Sender) *exporter {
	return &exporter{
		logger: logger,
		sender: sender,
		wg:     sync.WaitGroup{},
	}
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

	entry := &Entry{
		Level:   log.Level.String(),
		Time:    log.Time,
		Message: log.Message,
		Fields:  log.Data,
	}

	b := backoff.WithContext(backoff.WithMaxRetries(backoff.NewExponentialBackOff(), 3), ctx)
	err := backoff.Retry(func() error {
		return e.sender.SendLog(ctx, entry)
	}, b)

	if err != nil {
		e.logger.Debugf("sending logs: %v", err)
	}
}
