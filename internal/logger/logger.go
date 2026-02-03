package logger

import (
	"context"

	"github.com/sirupsen/logrus"
)

type ctxKey string

const loggerCtxKey ctxKey = "logger"

// FromContext retrieves the logger from context. If not present, returns the fallback logger.
func FromContext(ctx context.Context, fallback logrus.FieldLogger) logrus.FieldLogger {
	if logger, ok := ctx.Value(loggerCtxKey).(logrus.FieldLogger); ok && logger != nil {
		return logger
	}
	return fallback
}

// WithLogger returns a new context with the logger attached.
func WithLogger(ctx context.Context, logger logrus.FieldLogger) context.Context {
	return context.WithValue(ctx, loggerCtxKey, logger)
}
