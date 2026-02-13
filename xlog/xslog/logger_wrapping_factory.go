package xslog

import (
	"context"
	"log/slog"
	"runtime"
	"time"

	"github.com/ballastworks/xs/xcontext/xspan"
)

type LoggerWrappingFactory struct {
	f LoggerFactory
}

// NewLoggerWrappingFactory returns a new LoggerWrappingFactory that wraps the
// provided LoggerFactory and converts it into a Logger instance.
//
// In general this function should not be used. It is only useful for cases
// where a logger factory decorates the Logger(context.Context) method with
// additional work that should be delayed until a log record emitting function
// is called such as adding additional attributes or other emission time side
// effects.
//
// If the logger instance is highly reusable it is up to the implementation
// being wrapped to implement the cache mechanism and support concurrent
// access that is likely to occur in concurrent applications.
func NewLoggerWrappingFactory(factory LoggerFactory) *LoggerWrappingFactory {
	return &LoggerWrappingFactory{factory}
}

func (w *LoggerWrappingFactory) Enabled(ctx context.Context, level slog.Level) bool {
	return w.f.Enabled(ctx, level)
}

//go:noinline
func (w *LoggerWrappingFactory) Info(ctx context.Context, msg string, attrs ...slog.Attr) {
	const level = slog.LevelInfo

	if !w.f.Enabled(ctx, level) {
		return
	}

	now := time.Now().UTC()
	logger := w.f.Logger(ctx)

	pcs := [1]uintptr{}
	runtime.Callers(2, pcs[:])

	record := slog.NewRecord(now, level, msg, pcs[0])

	if len(attrs) != 0 {
		record.AddAttrs(attrs...)
	}

	logger.Handle(ctx, record)
}

//go:noinline
func (w *LoggerWrappingFactory) Debug(ctx context.Context, msg string, attrs ...slog.Attr) {
	const level = slog.LevelDebug

	if !w.f.Enabled(ctx, level) {
		return
	}

	now := time.Now().UTC()
	logger := w.f.Logger(ctx)

	pcs := [1]uintptr{}
	runtime.Callers(2, pcs[:])

	record := slog.NewRecord(now, level, msg, pcs[0])

	if len(attrs) != 0 {
		record.AddAttrs(attrs...)
	}

	logger.Handle(ctx, record)
}

//go:noinline
func (w *LoggerWrappingFactory) Error(ctx context.Context, msg string, attrs ...slog.Attr) {
	const level = slog.LevelError

	if !w.f.Enabled(ctx, level) {
		return
	}

	now := time.Now().UTC()
	logger := w.f.Logger(ctx)

	pcs := [1]uintptr{}
	runtime.Callers(2, pcs[:])

	record := slog.NewRecord(now, level, msg, pcs[0])

	if len(attrs) != 0 {
		record.AddAttrs(attrs...)
	}

	logger.Handle(ctx, record)
}

//go:noinline
func (w *LoggerWrappingFactory) Warn(ctx context.Context, msg string, attrs ...slog.Attr) {
	const level = slog.LevelWarn

	if !w.f.Enabled(ctx, level) {
		return
	}

	now := time.Now().UTC()
	logger := w.f.Logger(ctx)

	pcs := [1]uintptr{}
	runtime.Callers(2, pcs[:])

	record := slog.NewRecord(now, level, msg, pcs[0])

	if len(attrs) != 0 {
		record.AddAttrs(attrs...)
	}

	logger.Handle(ctx, record)
}

func (w *LoggerWrappingFactory) Handle(ctx context.Context, record slog.Record) error {
	logger := w.f.Logger(ctx)
	return logger.Handle(ctx, record)
}

//go:noinline
func (w *LoggerWrappingFactory) Log(ctx context.Context, level slog.Level, msg string, attrs ...slog.Attr) {

	if !w.f.Enabled(ctx, level) {
		return
	}

	now := time.Now().UTC()
	logger := w.f.Logger(ctx)

	pcs := [1]uintptr{}
	runtime.Callers(2, pcs[:])

	record := slog.NewRecord(now, level, msg, pcs[0])

	if len(attrs) != 0 {
		record.AddAttrs(attrs...)
	}

	logger.Handle(ctx, record)
}

// LogUnchecked is similar to Log except filtering (such as log level
// filtering) is disabled so such concerns go unchecked.
//
// The caller assumes the responsibility of filtering the log in the way they
// prefer before calling this function.
//
//go:noinline
func (w *LoggerWrappingFactory) LogUnchecked(ctx context.Context, level slog.Level, msg string, attrs ...slog.Attr) {
	now := time.Now().UTC()
	logger := w.f.Logger(ctx)

	pcs := [1]uintptr{}
	runtime.Callers(2, pcs[:])

	record := slog.NewRecord(now, level, msg, pcs[0])

	if len(attrs) != 0 {
		record.AddAttrs(attrs...)
	}

	logger.Handle(ctx, record)
}

//go:noinline
func (w *LoggerWrappingFactory) SpanErr(ctx context.Context, err error, msg string, attrs ...slog.Attr) {
	const level = slog.LevelError

	xspan.RecordError(ctx, err, msg)

	if !w.f.Enabled(ctx, level) {
		return
	}

	now := time.Now().UTC()
	logger := w.f.Logger(ctx)

	pcs := [1]uintptr{}
	runtime.Callers(2, pcs[:])

	record := slog.NewRecord(now, level, msg, pcs[0])

	if len(attrs) != 0 {
		record.AddAttrs(attrs...)
	}

	logger.Handle(ctx, record)
}

//go:noinline
func (w *LoggerWrappingFactory) SpanFail(ctx context.Context, err error, msg string, attrs ...slog.Attr) {
	const level = slog.LevelError

	xspan.Fail(ctx, err, msg)

	if !w.f.Enabled(ctx, level) {
		return
	}

	now := time.Now().UTC()
	logger := w.f.Logger(ctx)

	pcs := [1]uintptr{}
	runtime.Callers(2, pcs[:])

	record := slog.NewRecord(now, level, msg, pcs[0])

	if len(attrs) != 0 {
		record.AddAttrs(attrs...)
	}

	logger.Handle(ctx, record)
}

func (w *LoggerWrappingFactory) SlogHandler(ctx context.Context) slog.Handler {
	return w.f.Logger(ctx).SlogHandler(ctx)
}

func (w *LoggerWrappingFactory) WithAttrs(ctx context.Context, attrs ...slog.Attr) Logger {
	return w.f.Logger(ctx).WithAttrs(ctx, attrs...)
}

func (w *LoggerWrappingFactory) WithErr(ctx context.Context, err error) Logger {
	return w.f.Logger(ctx).WithErr(ctx, err)
}
