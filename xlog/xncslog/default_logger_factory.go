// xncslog contains exported global default functions similar to xslog, however
// they will never attempt to load logger factory defaults from the context.
//
// Most should not use this package, there are only small circumstances where
// loading the logger factory from the context which does contain one is
// undesirable.

package xncslog

import (
	"context"
	"log/slog"
	"runtime"
	"time"

	"github.com/ballastworks/xs/xcontext/xspan"
	"github.com/ballastworks/xs/xlog/xslog"
)

//
// global default funcs
//

func Enabled(ctx context.Context, level slog.Level) bool {
	return xslog.DefaultFactory().Enabled(ctx, level)
}

//go:noinline
func Debug(ctx context.Context, msg string, attrs ...slog.Attr) {
	const level = slog.LevelDebug

	logf := xslog.DefaultFactory()
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

	logf := xslog.DefaultFactory()
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

	logf := xslog.DefaultFactory()
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
	logf := xslog.DefaultFactory()
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

	logf := xslog.DefaultFactory()
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

	logf := xslog.DefaultFactory()
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

	logf := xslog.DefaultFactory()
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

func WithAttrs(ctx context.Context, attrs ...slog.Attr) xslog.Logger {
	logger := xslog.DefaultFactory().Logger(ctx)
	return logger.WithAttrs(ctx, attrs...)
}

func Handle(ctx context.Context, record slog.Record) {
	logger := xslog.DefaultFactory().Logger(ctx)
	logger.Handle(ctx, record)
}

func SlogHandler(ctx context.Context) slog.Handler {
	logger := xslog.DefaultFactory().Logger(ctx)
	return logger.SlogHandler(ctx)
}
