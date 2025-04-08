package logexporter

import (
	"context"
	"fmt"
	"path"
	"runtime"
	"sync"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/castai/cluster-controller/internal/castai"
	"github.com/castai/cluster-controller/internal/waitext"
)

const (
	sendTimeout = 15 * time.Second
)

// LogExporter hooks into logrus and sends logs to Mothership.
type LogExporter struct {
	logger *logrus.Logger
	sender castai.CastAIClient
	wg     sync.WaitGroup
}

// exporter must satisfy logrus.Hook.
var _ logrus.Hook = new(LogExporter)

func NewLogger(logLevel uint32) *logrus.Logger {
	logger := logrus.New()
	logger.SetLevel(logrus.Level(logLevel))
	logger.SetReportCaller(true)
	logger.Formatter = &logrus.TextFormatter{
		CallerPrettyfier: func(f *runtime.Frame) (string, string) {
			filename := path.Base(f.File)
			return fmt.Sprintf("%s()", f.Function), fmt.Sprintf("%s:%d", filename, f.Line)
		},
	}

	return logger
}

func SetupLogExporter(logger *logrus.Logger, sender castai.CastAIClient) {
	logExporter := newLogExporter(logger, sender)
	logger.AddHook(logExporter)
	logrus.RegisterExitHandler(logExporter.Wait)
}

// NewLogExporter returns new exporter that can be hooked into logrus
// to inject logs into Cast AI.
func newLogExporter(logger *logrus.Logger, sender castai.CastAIClient) *LogExporter {
	return &LogExporter{
		logger: logger,
		sender: sender,
		wg:     sync.WaitGroup{},
	}
}

// Levels lists levels that tell logrus to trigger log injection.
func (e *LogExporter) Levels() []logrus.Level {
	return []logrus.Level{
		logrus.ErrorLevel,
		logrus.FatalLevel,
		logrus.PanicLevel,
		logrus.InfoLevel,
		logrus.WarnLevel,
	}
}

// Fire called by logrus with log entry that LogExporter sends out.
func (e *LogExporter) Fire(entry *logrus.Entry) error {
	e.wg.Add(1)

	// logrus accesses fields of *Entry internally
	// -> we create our own struct _before_ releasing the hook instead of inside the goroutine
	// -> this avoids data races with logrus accessing the entry as well.
	castLogEntry := &castai.LogEntry{
		Level:   entry.Level.String(),
		Time:    entry.Time,
		Message: entry.Message,
	}
	castLogEntry.Fields = make(logrus.Fields, len(entry.Data))
	for k, v := range entry.Data {
		castLogEntry.Fields[k] = v
	}

	go func(entry *castai.LogEntry) {
		defer e.wg.Done()
		e.sendLogEvent(entry)
	}(castLogEntry)

	return nil
}

// Wait lets all pending log sends to finish.
func (e *LogExporter) Wait() {
	e.wg.Wait()
}

func (e *LogExporter) sendLogEvent(log *castai.LogEntry) {
	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()

	// Server expects fields values to be strings. If they're not it fails with BAD_REQUEST/400.
	// Alternatively we could use "google/protobuf/any.proto" on server side but ATM it doesn't work.
	for k, v := range log.Fields {
		switch v.(type) {
		case string:
		// do nothing
		default:
			log.Fields[k] = fmt.Sprint(v) // Force into string
		}
	}

	b := waitext.DefaultExponentialBackoff()
	err := waitext.Retry(ctx, b, 3, func(ctx context.Context) (bool, error) {
		return true, e.sender.SendLog(ctx, log)
	}, func(err error) {
		e.logger.Debugf("failed to send logs, will retry: %s", err)
	})
	if err != nil {
		e.logger.Debugf("sending logs: %v", err)
	}
}
