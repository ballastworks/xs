package ctx_slog

import (
	"context"
)

type loggerFactoryCtxKey struct{}

// LoggerFactoryFromContext returns a xslog.LoggerFactory implementation or nil
func LoggerFactoryFromContext(ctx context.Context) any {
	return ctx.Value(loggerFactoryCtxKey{})
}

func ContextWithLoggerFactory(ctx context.Context, factory any) context.Context {
	// invariant: factory must be a xslog.LoggerFactory implementing instance

	return context.WithValue(ctx, loggerFactoryCtxKey{}, factory)
}
