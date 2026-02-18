package xslog

import (
	"context"
	"encoding/hex"
	"log/slog"
	"runtime"
	"time"

	"github.com/ballastworks/xs/xcontext/xspan"
	"go.opentelemetry.io/otel/trace"
)

type structLogger struct {
	handler    slog.Handler
	level      slog.Level
	levelValid bool
}

// WithErr ignores the context and adds error attributes to a new logger instance.
func (s *structLogger) WithErr(_ context.Context, err error) Logger {
	return s.withAttrs(errAttrs(err)...)
}

// Handle always returns a nil error
func (s *structLogger) Handle(ctx context.Context, record slog.Record) error {
	// cloning to clip the internal attrs slice and prevent accidental side effects on the original record since we will be modifying the attrs slice by adding group attributes to it.
	record = record.Clone()

	return logRecord(ctx, s.handler, record)
}

// helpful refs:
// https://pkg.go.dev/golang.org/x/exp/slog#hdr-Groups
// https://pkg.go.dev/golang.org/x/exp/slog#Logger.WithGroup
//
// IMHO, opt-out unless logs are running on third party systems and source
// context should be hidden for various reasons.

// WithGroup is intentionally not implemented because it is not something
// we want to support in this logger implementation without additional
// careful consideration. Other strategies can be used to achieve similar
// behaviors if needed.
//
// func (s *structLogger) WithGroup(name string) ??? {
// 	return s.logger.WithGroup(name)
// }

//
// end slog.Handler specific Implementations
//

func (s *structLogger) Enabled(ctx context.Context, level slog.Level) bool {
	if s.levelValid {
		return (level >= s.level)
	}

	return s.handler.Enabled(ctx, level)
}

func logRecord(ctx context.Context, handler slog.Handler, record slog.Record) error {

	const (
		logKeyFilePath = "code.file.path"
		logKeyFileLine = "code.line.number"
		logKeyFuncName = "code.function.name"
	)

	// alloc: 2; 232 bytes
	var f runtime.Frame
	{
		framesIter := runtime.CallersFrames([]uintptr{record.PC})
		f, _ = framesIter.Next()
	}

	// alloc: 1; 16 bytes from SpanContextFromContext operation
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		arrTraceID := [16 * 2]byte{}
		arrTraceFlags := [1 * 2]byte{}
		arrSpanID := [8 * 2]byte{}

		var traceID, traceFlags, spanID []byte
		{
			tid := sc.TraceID()
			traceID = hex.AppendEncode(arrTraceID[:0], tid[:])

			arrTraceFlags[0] = byte(sc.TraceFlags())
			traceFlags = hex.AppendEncode(arrTraceFlags[:0], arrTraceFlags[:1])

			sid := sc.SpanID()
			spanID = hex.AppendEncode(arrSpanID[:0], sid[:])
		}

		// alloc: 2; 48 bytes
		record.AddAttrs(
			slog.String(logKeyFilePath, f.File),
			slog.String(logKeyFuncName, f.Function),
			slog.Int(logKeyFileLine, f.Line),
			slog.String("trace_id", string(traceID)),
			slog.String("trace_flags", string(traceFlags)),
			slog.String("span_id", string(spanID)),
		)
	} else {
		// zero allocs
		record.AddAttrs(
			slog.String(logKeyFilePath, f.File),
			slog.String(logKeyFuncName, f.Function),
			slog.Int(logKeyFileLine, f.Line),
		)
	}

	// zero allocs
	return handler.Handle(ctx, record)
}

func logNewRecord(ctx context.Context, handler slog.Handler, pc uintptr, time time.Time, level slog.Level, msg string, attrs ...slog.Attr) error {
	// zero allocs
	record := slog.NewRecord(time, level, msg, pc)

	// alloc: 2; 48 bytes
	record.AddAttrs(attrs...)

	// zero allocs
	return logRecord(ctx, handler, record)
}

func (s *structLogger) handle(ctx context.Context, pc uintptr, time time.Time, level slog.Level, msg string, attrs ...slog.Attr) {
	if err := logNewRecord(ctx, s.handler, pc, time, level, msg, attrs...); err != nil {
		// ignoring - not much we could do here except print to stderr... TODO: consider adding a fallback option going to stderr?

		// ignore the error, but at least record in the span
		xspan.RecordError(ctx, err, "")
	}
}

//go:noinline
func (s *structLogger) Debug(ctx context.Context, msg string, attrs ...slog.Attr) {
	const level = slog.LevelDebug

	if !s.Enabled(ctx, level) {
		return
	}

	now := time.Now().UTC()

	pcs := [1]uintptr{}
	runtime.Callers(2, pcs[:])

	s.handle(ctx, pcs[0], now, level, msg, attrs...)
}

//go:noinline
func (s *structLogger) Error(ctx context.Context, msg string, attrs ...slog.Attr) {
	const level = slog.LevelError

	if !s.Enabled(ctx, level) {
		return
	}

	now := time.Now().UTC()

	pcs := [1]uintptr{}
	runtime.Callers(2, pcs[:])

	s.handle(ctx, pcs[0], now, level, msg, attrs...)
}

//go:noinline
func (s *structLogger) Info(ctx context.Context, msg string, attrs ...slog.Attr) {
	const level = slog.LevelInfo

	if !s.Enabled(ctx, level) {
		return
	}

	now := time.Now().UTC()

	pcs := [1]uintptr{}
	runtime.Callers(2, pcs[:])

	s.handle(ctx, pcs[0], now, level, msg, attrs...)
}

//go:noinline
func (s *structLogger) Warn(ctx context.Context, msg string, attrs ...slog.Attr) {
	const level = slog.LevelWarn

	if !s.Enabled(ctx, level) {
		return
	}

	now := time.Now().UTC()

	pcs := [1]uintptr{}
	runtime.Callers(2, pcs[:])

	s.handle(ctx, pcs[0], now, level, msg, attrs...)
}

//go:noinline
func (s *structLogger) SpanErr(ctx context.Context, err error, msg string, attrs ...slog.Attr) {
	const level = slog.LevelError

	xspan.RecordError(ctx, err, msg)

	if !s.Enabled(ctx, level) {
		return
	}

	now := time.Now().UTC()

	pcs := [1]uintptr{}
	runtime.Callers(2, pcs[:])

	s.handle(ctx, pcs[0], now, level, msg, attrs...)
}

//go:noinline
func (s *structLogger) SpanFail(ctx context.Context, err error, msg string, attrs ...slog.Attr) {
	const level = slog.LevelError

	xspan.Fail(ctx, err, msg)

	if !s.Enabled(ctx, level) {
		return
	}

	now := time.Now().UTC()

	pcs := [1]uintptr{}
	runtime.Callers(2, pcs[:])

	s.handle(ctx, pcs[0], now, level, msg, attrs...)
}

//go:noinline
func (s *structLogger) Log(ctx context.Context, level slog.Level, msg string, attrs ...slog.Attr) {
	if !s.Enabled(ctx, level) {
		return
	}

	now := time.Now().UTC()

	pcs := [1]uintptr{}
	runtime.Callers(2, pcs[:])

	s.handle(ctx, pcs[0], now, level, msg, attrs...)
}

// LogUnchecked is similar to Log except filtering (such as log level
// filtering) is disabled so such concerns go unchecked.
//
// The caller assumes the responsibility of filtering the log in the way they
// prefer before calling this function.
//
//go:noinline
func (s *structLogger) LogUnchecked(ctx context.Context, level slog.Level, msg string, attrs ...slog.Attr) {
	now := time.Now().UTC()

	pcs := [1]uintptr{}
	runtime.Callers(2, pcs[:])

	s.handle(ctx, pcs[0], now, level, msg, attrs...)
}

func (s *structLogger) withAttrs(attrs ...slog.Attr) *structLogger {
	if len(attrs) == 0 {
		return s
	}

	h := s.handler.WithAttrs(attrs)

	return &structLogger{h, s.level, s.levelValid}
}

// WithAttrs ignores the context and returns a logger instance with the
// attributes added to it.
func (s *structLogger) WithAttrs(_ context.Context, attrs ...slog.Attr) Logger {
	return s.withAttrs(attrs...)
}

// SlogHandler ignores the context and wraps the structured logger in a
// presentation layer that implements log.Handler.
func (s *structLogger) SlogHandler(context.Context) slog.Handler {
	return slogStructLogger{s}
}

func (s *structLogger) withAttrsAsSlogHandler(attrs ...slog.Attr) slog.Handler {
	return slogStructLogger{s.withAttrs(attrs...)}
}

type slogLinker interface {
	Enabled(ctx context.Context, level slog.Level) bool
	Handle(ctx context.Context, record slog.Record) error
	withAttrsAsSlogHandler(attrs ...slog.Attr) slog.Handler
}

type slogStructLogger struct {
	w slogLinker
}

//
// slogStructLogger implementation
//

func (s slogStructLogger) Enabled(ctx context.Context, level slog.Level) bool {
	return s.w.Enabled(ctx, level)
}

func (s slogStructLogger) Handle(ctx context.Context, record slog.Record) error {
	return s.w.Handle(ctx, record)
}

func (s slogStructLogger) WithGroup(name string) slog.Handler {
	if name == "" {
		return s
	}

	var level slog.Level
	var levelValid bool
	switch v := s.w.(type) {
	case *structLogger:
		level = v.level
		levelValid = v.levelValid
	case *structLoggerWriteTracked:
		level = v.w.level
		levelValid = v.w.levelValid
	default:
		panic("unexpected logger type in slogStructLogger.WithGroup")
	}

	return &structLoggerGrouped{s, name, nil, level, levelValid}
}

func (s slogStructLogger) WithAttrs(attrs []slog.Attr) slog.Handler {
	return s.w.withAttrsAsSlogHandler(attrs...)
}
