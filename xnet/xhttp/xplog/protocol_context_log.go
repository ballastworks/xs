// xplog - the structured logger for the application layer http protocol associated package xhttp located at xhttp/xplog
package xplog

import (
	"context"
	"log/slog"
	"net/http"
	"runtime"
	"time"

	"github.com/ballastworks/xs/internal/ctx_slog"
	"github.com/ballastworks/xs/xcontext/xspan"
	"github.com/ballastworks/xs/xlog/xslog"
	"github.com/ballastworks/xs/xnet/xhttp"
)

//
// global default funcs
//

func loggerFactoryFromContextOrDefault(ctx context.Context) xslog.LoggerFactory {
	if v := ctx_slog.LoggerFactoryFromContext(ctx); v != nil {
		return v.(xslog.LoggerFactory)
	}

	return xhttp.DefaultLoggerFactory()
}

// ProtoLogger returns a Logger stripped of passing context concerns by pulling
// that from the protocol request object via req.Context(). The logger factory
// chosen to initialize the logger instances the wrapper creates are also
// chosen based on this context.
//
// It exists as syntactic sugar for persons that choose to take responsibility
// of managing the lifetime of the logger resource and not misuse it outside of
// the lifecycle of the context it wraps.
//
// It is dangerous to pass instances of this logger around too far from where
// it was initialized and beyond the scope of the context it wraps around.
//
// It should only be used in a short-lived fashion for a protocol specific
// context where it could be used for several different log emissions.
//
// Otherwise use xplog.Logger(req.Context())
func ProtoLogger(req *http.Request) *xslog.LoggerWrappingContextFactory {
	ctx := req.Context()
	logf := loggerFactoryFromContextOrDefault(ctx)
	return xslog.NewLoggerWrappingContextFactory(ctx, logf)
}

func Logger(ctx context.Context) xslog.Logger {
	logf := loggerFactoryFromContextOrDefault(ctx)
	return logf.Logger(ctx)
}

// ProtoLogger returns a logger factory chosen based on the context within the
// protocol request object via req.Context().
func ProtoLoggerFactory(req *http.Request) xslog.LoggerFactory {
	ctx := req.Context()
	return loggerFactoryFromContextOrDefault(ctx)
}

func LoggerFactory(ctx context.Context) xslog.LoggerFactory {
	return loggerFactoryFromContextOrDefault(ctx)
}

func Enabled(req *http.Request, level slog.Level) bool {
	ctx := req.Context()
	return loggerFactoryFromContextOrDefault(ctx).Enabled(ctx, level)
}

//go:noinline
func Debug(req *http.Request, msg string, attrs ...slog.Attr) {
	const level = slog.LevelDebug

	ctx := req.Context()

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
func Error(req *http.Request, msg string, attrs ...slog.Attr) {
	const level = slog.LevelError

	ctx := req.Context()

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
func Info(req *http.Request, msg string, attrs ...slog.Attr) {
	const level = slog.LevelInfo

	ctx := req.Context()

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
func Log(req *http.Request, level slog.Level, msg string, attrs ...slog.Attr) {
	ctx := req.Context()

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
func Warn(req *http.Request, msg string, attrs ...slog.Attr) {
	const level = slog.LevelWarn

	ctx := req.Context()

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
func SpanErr(req *http.Request, err error, msg string, attrs ...slog.Attr) {
	const level = slog.LevelError

	ctx := req.Context()

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

	if len(attrs) != 0 {
		record.AddAttrs(attrs...)
	}

	logger.Handle(ctx, record)
}

//go:noinline
func SpanFail(req *http.Request, err error, msg string, attrs ...slog.Attr) {
	const level = slog.LevelError

	ctx := req.Context()

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

	if len(attrs) != 0 {
		record.AddAttrs(attrs...)
	}

	logger.Handle(ctx, record)
}

func WithAttrs(req *http.Request, attrs ...slog.Attr) xslog.Logger {
	ctx := req.Context()

	logger := loggerFactoryFromContextOrDefault(ctx).Logger(ctx)
	return logger.WithAttrs(ctx, attrs...)
}

func Handle(req *http.Request, record slog.Record) {
	ctx := req.Context()

	logger := loggerFactoryFromContextOrDefault(ctx).Logger(ctx)
	logger.Handle(ctx, record)
}

func SlogHandler(req *http.Request) slog.Handler {
	ctx := req.Context()

	logger := loggerFactoryFromContextOrDefault(ctx).Logger(ctx)
	return logger.SlogHandler(ctx)
}
