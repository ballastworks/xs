package xslog

import (
	"context"
	"log/slog"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/ballastworks/xs/xcontext/xspan"
)

type structLoggerWriteTracked struct {
	w             *structLogger
	handledRecPtr *atomic.Bool
	handledRec    atomic.Bool
}

func (s *structLoggerWriteTracked) trackRecordWritten() {
	if s.handledRecPtr != nil {
		s.handledRecPtr.Store(true)
		return
	}

	s.handledRec.Store(true)
}

func (s *structLoggerWriteTracked) recordWritten() bool {
	if s.handledRecPtr != nil {
		return s.handledRecPtr.Load()
	}

	return s.handledRec.Load()
}

func (s *structLoggerWriteTracked) Enabled(ctx context.Context, level slog.Level) bool {
	return s.w.Enabled(ctx, level)
}

func (s *structLoggerWriteTracked) handle(ctx context.Context, pc uintptr, time time.Time, level slog.Level, msg string, attrs ...slog.Attr) {
	s.trackRecordWritten()
	s.w.handle(ctx, pc, time, level, msg, attrs...)
}

func (s *structLoggerWriteTracked) Handle(ctx context.Context, record slog.Record) error {
	s.trackRecordWritten()
	return s.w.Handle(ctx, record)
}

//go:noinline
func (s *structLoggerWriteTracked) Debug(ctx context.Context, msg string, attrs ...slog.Attr) {
	const level = slog.LevelDebug

	if !s.w.Enabled(ctx, level) {
		return
	}

	now := time.Now().UTC()

	pcs := [1]uintptr{}
	runtime.Callers(2, pcs[:])

	s.handle(ctx, pcs[0], now, level, msg, attrs...)
}

//go:noinline
func (s *structLoggerWriteTracked) Error(ctx context.Context, msg string, attrs ...slog.Attr) {
	const level = slog.LevelError

	if !s.w.Enabled(ctx, level) {
		return
	}

	now := time.Now().UTC()

	pcs := [1]uintptr{}
	runtime.Callers(2, pcs[:])

	s.handle(ctx, pcs[0], now, level, msg, attrs...)
}

//go:noinline
func (s *structLoggerWriteTracked) Info(ctx context.Context, msg string, attrs ...slog.Attr) {
	const level = slog.LevelInfo

	if !s.w.Enabled(ctx, level) {
		return
	}

	now := time.Now().UTC()

	pcs := [1]uintptr{}
	runtime.Callers(2, pcs[:])

	s.handle(ctx, pcs[0], now, level, msg, attrs...)
}

//go:noinline
func (s *structLoggerWriteTracked) Warn(ctx context.Context, msg string, attrs ...slog.Attr) {
	const level = slog.LevelWarn

	if !s.w.Enabled(ctx, level) {
		return
	}

	now := time.Now().UTC()

	pcs := [1]uintptr{}
	runtime.Callers(2, pcs[:])

	s.handle(ctx, pcs[0], now, level, msg, attrs...)
}

//go:noinline
func (s *structLoggerWriteTracked) Log(ctx context.Context, level slog.Level, msg string, attrs ...slog.Attr) {
	if !s.w.Enabled(ctx, level) {
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
func (s *structLoggerWriteTracked) LogUnchecked(ctx context.Context, level slog.Level, msg string, attrs ...slog.Attr) {
	now := time.Now().UTC()

	pcs := [1]uintptr{}
	runtime.Callers(2, pcs[:])

	s.handle(ctx, pcs[0], now, level, msg, attrs...)
}

//go:noinline
func (s *structLoggerWriteTracked) SpanErr(ctx context.Context, err error, msg string, attrs ...slog.Attr) {
	const level = slog.LevelError

	xspan.RecordError(ctx, err, msg)

	if !s.w.Enabled(ctx, level) {
		return
	}

	now := time.Now().UTC()

	pcs := [1]uintptr{}
	runtime.Callers(2, pcs[:])

	s.handle(ctx, pcs[0], now, level, msg, attrs...)
}

//go:noinline
func (s *structLoggerWriteTracked) SpanFail(ctx context.Context, err error, msg string, attrs ...slog.Attr) {
	const level = slog.LevelError

	xspan.Fail(ctx, err, msg)

	if !s.w.Enabled(ctx, level) {
		return
	}

	now := time.Now().UTC()

	pcs := [1]uintptr{}
	runtime.Callers(2, pcs[:])

	s.handle(ctx, pcs[0], now, level, msg, attrs...)
}

func (s *structLoggerWriteTracked) withAttrs(attrs ...slog.Attr) *structLoggerWriteTracked {
	if len(attrs) == 0 {
		return s
	}

	h := s.w.handler.WithAttrs(attrs)

	innerStructLogger := &structLogger{h, s.w.level, s.w.levelValid}

	handledRecPtr := s.handledRecPtr
	if handledRecPtr == nil {
		handledRecPtr = &s.handledRec
	}

	return &structLoggerWriteTracked{w: innerStructLogger, handledRecPtr: handledRecPtr}
}

// WithAttrs ignores the context and returns a logger instance with the
// attributes added to it.
func (s *structLoggerWriteTracked) WithAttrs(_ context.Context, attrs ...slog.Attr) Logger {
	return s.withAttrs(attrs...)
}

// SlogHandler ignores the context and wraps the structured logger in a
// presentation layer that implements log.Handler.
func (s *structLoggerWriteTracked) SlogHandler(context.Context) slog.Handler {
	return slogStructLogger{s}
}

func (s *structLoggerWriteTracked) withAttrsAsSlogHandler(attrs ...slog.Attr) slog.Handler {
	return slogStructLogger{s.withAttrs(attrs...)}
}

// WithErr ignores the context and adds error attributes to a new logger instance.
func (s *structLoggerWriteTracked) WithErr(_ context.Context, err error) Logger {
	return s.withAttrs(errAttrs(err)...)
}

// slog.Handler types:
// - *structLoggerGrouped (wraps a *structLoggerGrouped or a slogStructLogger)
// - slogStructLogger (wraps *structLogger or *structLoggerWriteTracked)

type simpleHandler interface {
	// Enabled returns true if the logger would emit a log for the supplied
	// level.
	Enabled(ctx context.Context, level slog.Level) bool

	// Handle emits a given log record if the level of the record is enabled
	// for a given context.
	Handle(ctx context.Context, record slog.Record) error
}

// RecordWritten takes a logger handler and returns true if the handler emitted
// a log record. It will only return true if the logger was initialized with
// TrackWrites(true) or created from a request logger factory initialized with
// TrackWrites(true).
//
// When the MiddlewareLogger option EmitRequestCorrelationLogs(true) is set,
// then the middleware will call this function.
func RecordWritten(h simpleHandler) bool {
	type recordWriteTracker interface{ recordWritten() bool }

	var vt recordWriteTracker
	for {

		if v, ok := h.(recordWriteTracker); ok {
			vt = v
		}

		switch v := h.(type) {
		case interface{ Handler() slog.Handler }:
			h = v.Handler()
			continue
		case *structLoggerGrouped:
			h = v.handler
			continue
		case slogStructLogger:
			if v, ok := v.w.(recordWriteTracker); ok {
				vt = v
			}
		}

		break
	}
	if vt == nil {
		return false
	}

	return vt.recordWritten()
}
