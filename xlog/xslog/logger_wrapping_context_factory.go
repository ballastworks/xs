package xslog

import (
	"context"
	"log/slog"
	"runtime"
	"time"

	"github.com/ballastworks/xs/xcontext/xspan"
)

// LoggerWrappingContextFactory is an implementation of a Logger that wraps a
// LoggerFactory and an applicable context and changes the function signatures
// to not need context passed in.
//
// It exists as syntactic sugar for persons that choose to take responsibility
// of managing the lifetime of the logger resource and not misuse it outside of
// the lifecycle of the context it wraps.
//
// It is dangerous to pass instances of this logger around too far from where
// it was initialized and beyond the scope of the context it wraps around.
//
// It should only be used in a short-lived fashion for a protocol specific
// context where it could be used for several different log emissions if the
// wrapped logger factory has also implemented caching or very cheaply
// constructs the logger instance it returns.
//
// For all other cases, it is not worth the additional risk it subtly
// introduces for the complexity it attempts to hide in a naively simpler
// implementation surface area exposed to the user.
//
// Use at your own risk.
type LoggerWrappingContextFactory struct {
	ctx context.Context
	f   LoggerFactory
}

// NewLoggerWrappingContextFactory returns an implementation of a Logger that wraps a
// LoggerFactory and an applicable context and changes the function signatures
// to not need context passed in.
//
// It exists as syntactic sugar for persons that choose to take responsibility
// of managing the lifetime of the logger resource and not misuse it outside of
// the lifecycle of the context it wraps.
//
// It is dangerous to pass instances of this logger around too far from where
// it was initialized and beyond the scope of the context it wraps around.
//
// It should only be used in a short-lived fashion for a protocol specific
// context where it could be used for several different log emissions if the
// wrapped logger factory has also implemented caching or very cheaply
// constructs the logger instance it returns.
//
// For all other cases, it is not worth the additional risk it subtly
// introduces for the complexity it attempts to hide in a naively simpler
// implementation surface area exposed to the user.
//
// Use at your own risk.
func NewLoggerWrappingContextFactory(ctx context.Context, factory LoggerFactory) *LoggerWrappingContextFactory {
	return &LoggerWrappingContextFactory{ctx, factory}
}

func (w *LoggerWrappingContextFactory) Enabled(level slog.Level) bool {
	return w.f.Enabled(w.ctx, level)
}

//go:noinline
func (w *LoggerWrappingContextFactory) Info(msg string, attrs ...slog.Attr) {
	const level = slog.LevelInfo

	ctx := w.ctx

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
func (w *LoggerWrappingContextFactory) Debug(msg string, attrs ...slog.Attr) {
	const level = slog.LevelDebug

	ctx := w.ctx

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
func (w *LoggerWrappingContextFactory) Error(msg string, attrs ...slog.Attr) {
	const level = slog.LevelError

	ctx := w.ctx

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
func (w *LoggerWrappingContextFactory) Warn(msg string, attrs ...slog.Attr) {
	const level = slog.LevelWarn

	ctx := w.ctx

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

// Note that in general this function should not be used.
//
// It exists only only to enable some compatibility edge cases.
func (w *LoggerWrappingContextFactory) Handle(record slog.Record) error {
	ctx := w.ctx

	logger := w.f.Logger(ctx)
	return logger.Handle(ctx, record)
}

//go:noinline
func (w *LoggerWrappingContextFactory) Log(level slog.Level, msg string, attrs ...slog.Attr) {
	ctx := w.ctx

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
func (w *LoggerWrappingContextFactory) LogUnchecked(level slog.Level, msg string, attrs ...slog.Attr) {
	ctx := w.ctx

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
func (w *LoggerWrappingContextFactory) SpanErr(err error, msg string, attrs ...slog.Attr) {
	const level = slog.LevelError

	ctx := w.ctx

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
func (w *LoggerWrappingContextFactory) SpanFail(err error, msg string, attrs ...slog.Attr) {
	const level = slog.LevelError

	ctx := w.ctx

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

func (w *LoggerWrappingContextFactory) SlogHandler() slog.Handler {
	return w.f.Logger(w.ctx).SlogHandler(w.ctx)
}

func (w *LoggerWrappingContextFactory) WithAttrs(attrs ...slog.Attr) Logger {
	return w.f.Logger(w.ctx).WithAttrs(w.ctx, attrs...)
}

func (w *LoggerWrappingContextFactory) WithErr(err error) Logger {
	return w.f.Logger(w.ctx).WithErr(w.ctx, err)
}

func (w *LoggerWrappingContextFactory) Factory() LoggerFactory {
	return w.f
}
