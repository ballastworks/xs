package xslog

import (
	"context"
	"log/slog"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/ballastworks/xs/internal/ctx_slog"
	"github.com/ballastworks/xs/xcontext/xspan"
)

type boxedLoggerFactory struct {
	v LoggerFactory
}

func checkNewDefaultLoggerFactoryAtomic(err error) {
	if err != nil {
		panic(err)
	}
}

func newDefaultLoggerFactoryAtomic() atomic.Value {
	logf, err := NewFactory()
	checkNewDefaultLoggerFactoryAtomic(err)

	var v atomic.Value
	v.Store(boxedLoggerFactory{logf})
	return v
}

var defaultLoggerFactoryAtomic = newDefaultLoggerFactoryAtomic()

func DefaultFactory() LoggerFactory {
	return defaultLoggerFactoryAtomic.Load().(boxedLoggerFactory).v
}

// SetDefaultFactory replaces the old default response factory and returns the old instance
func SetDefaultFactory(factory LoggerFactory) LoggerFactory {
	if factory == nil {
		panic("nil logger factory")
	}

	old := defaultLoggerFactoryAtomic.Swap(boxedLoggerFactory{factory})
	return old.(boxedLoggerFactory).v
}

//
// global default funcs
//

func loggerFactoryFromContextOrDefault(ctx context.Context) LoggerFactory {
	if v := ctx_slog.LoggerFactoryFromContext(ctx); v != nil {
		return v.(LoggerFactory)
	}

	return DefaultFactory()
}

// ContextDefaultLoggerFactory returns a LoggerFactory value from the context or the default factory if not found
func ContextDefaultLoggerFactory(ctx context.Context) LoggerFactory {
	return loggerFactoryFromContextOrDefault(ctx)
}

func Enabled(ctx context.Context, level slog.Level) bool {
	return loggerFactoryFromContextOrDefault(ctx).Enabled(ctx, level)
}

//go:noinline
func Debug(ctx context.Context, msg string, attrs ...slog.Attr) {
	const level = slog.LevelDebug

	logf := loggerFactoryFromContextOrDefault(ctx)
	if !logf.Enabled(ctx, level) {
		return
	}

	now := time.Now().UTC()
	logger := logf.Logger(ctx)

	pcs := [1]uintptr{}
	runtime.Callers(2, pcs[:])

	record := slog.NewRecord(now, level, msg, pcs[0])

	if len(attrs) != 0 {
		record.AddAttrs(attrs...)
	}

	logger.Handle(ctx, record)
}

//go:noinline
func Error(ctx context.Context, msg string, attrs ...slog.Attr) {
	const level = slog.LevelError

	logf := loggerFactoryFromContextOrDefault(ctx)
	if !logf.Enabled(ctx, level) {
		return
	}

	now := time.Now().UTC()
	logger := logf.Logger(ctx)

	pcs := [1]uintptr{}
	runtime.Callers(2, pcs[:])

	record := slog.NewRecord(now, level, msg, pcs[0])

	if len(attrs) != 0 {
		record.AddAttrs(attrs...)
	}

	logger.Handle(ctx, record)
}

//go:noinline
func Info(ctx context.Context, msg string, attrs ...slog.Attr) {
	const level = slog.LevelInfo

	logf := loggerFactoryFromContextOrDefault(ctx)
	if !logf.Enabled(ctx, level) {
		return
	}

	now := time.Now().UTC()
	logger := logf.Logger(ctx)

	pcs := [1]uintptr{}
	runtime.Callers(2, pcs[:])

	record := slog.NewRecord(now, level, msg, pcs[0])

	if len(attrs) != 0 {
		record.AddAttrs(attrs...)
	}

	logger.Handle(ctx, record)
}

//go:noinline
func Log(ctx context.Context, level slog.Level, msg string, attrs ...slog.Attr) {
	logf := loggerFactoryFromContextOrDefault(ctx)
	if !logf.Enabled(ctx, level) {
		return
	}

	now := time.Now().UTC()
	logger := logf.Logger(ctx)

	pcs := [1]uintptr{}
	runtime.Callers(2, pcs[:])

	record := slog.NewRecord(now, level, msg, pcs[0])

	if len(attrs) != 0 {
		record.AddAttrs(attrs...)
	}

	logger.Handle(ctx, record)
}

//go:noinline
func Warn(ctx context.Context, msg string, attrs ...slog.Attr) {
	const level = slog.LevelWarn

	logf := loggerFactoryFromContextOrDefault(ctx)
	if !logf.Enabled(ctx, level) {
		return
	}

	now := time.Now().UTC()
	logger := logf.Logger(ctx)

	pcs := [1]uintptr{}
	runtime.Callers(2, pcs[:])

	record := slog.NewRecord(now, level, msg, pcs[0])

	if len(attrs) != 0 {
		record.AddAttrs(attrs...)
	}

	logger.Handle(ctx, record)
}

//go:noinline
func SpanErr(ctx context.Context, err error, msg string, attrs ...slog.Attr) {
	const level = slog.LevelError

	xspan.RecordError(ctx, err, msg)

	logf := loggerFactoryFromContextOrDefault(ctx)
	if !logf.Enabled(ctx, level) {
		return
	}

	now := time.Now().UTC()
	logger := logf.Logger(ctx)

	pcs := [1]uintptr{}
	runtime.Callers(2, pcs[:])

	record := slog.NewRecord(now, level, msg, pcs[0])

	logger.Handle(ctx, record)
}

//go:noinline
func SpanFail(ctx context.Context, err error, msg string, attrs ...slog.Attr) {
	const level = slog.LevelError

	xspan.Fail(ctx, err, msg)

	logf := loggerFactoryFromContextOrDefault(ctx)
	if !logf.Enabled(ctx, level) {
		return
	}

	now := time.Now().UTC()
	logger := logf.Logger(ctx)

	pcs := [1]uintptr{}
	runtime.Callers(2, pcs[:])

	record := slog.NewRecord(now, level, msg, pcs[0])

	logger.Handle(ctx, record)
}

func WithAttrs(ctx context.Context, attrs ...slog.Attr) Logger {
	logger := loggerFactoryFromContextOrDefault(ctx).Logger(ctx)
	return logger.WithAttrs(ctx, attrs...)
}

func Handle(ctx context.Context, record slog.Record) {
	logger := loggerFactoryFromContextOrDefault(ctx).Logger(ctx)
	logger.Handle(ctx, record)
}

func SlogHandler(ctx context.Context) slog.Handler {
	logger := loggerFactoryFromContextOrDefault(ctx).Logger(ctx)
	return logger.SlogHandler(ctx)
}
