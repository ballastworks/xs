package xslog

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
)

const (
	panicUnexpectedLoggerTypeInSlogStructLogger = "unexpected logger type in slogStructLogger"
)

// TODO: consider "duplicating" internal calls into their public call site wrappers to increase inlining and reduce allocations - this requires more performance testing after initial implementation is complete.

var (
	ErrBadLoggerConfig = errors.New("bad logger config")
)

type loggerConfig struct {
	level      slog.Level
	handler    slog.Handler
	stream     io.Writer
	levelSet   bool
	handlerSet bool
	streamSet  bool

	trackWrites bool
}

func (cfg *loggerConfig) validate() error {
	if cfg.handlerSet {

		if cfg.handler == nil {
			return errors.New("nil handler specified")
		}

		if cfg.streamSet {
			return errors.New("cannot specify both a handler and a stream")
		}

		return nil
	}

	stream := cfg.stream

	if !cfg.streamSet {
		stream = io.Writer(os.Stderr)
	} else if stream == nil {
		return errors.New("nil stream specified")
	}

	cfg.handler = slog.NewJSONHandler(stream, &slog.HandlerOptions{
		Level: cfg.level,
	})

	return nil
}

type LoggerOption func(*loggerConfig)

func LoggerOpts() loggerOpts {
	return loggerOpts{}
}

type loggerOpts struct{}

func (loggerOpts) Level(level slog.Level) LoggerOption {
	return func(cfg *loggerConfig) {
		cfg.level = level
		cfg.levelSet = true
	}
}

func (loggerOpts) Handler(h slog.Handler) LoggerOption {
	return func(cfg *loggerConfig) {
		cfg.handler = h
		cfg.handlerSet = true
	}
}

func (loggerOpts) Stream(w io.Writer) LoggerOption {
	return func(cfg *loggerConfig) {
		cfg.stream = w
		cfg.streamSet = true
	}
}

// TrackWrites should not be called under most circumstances by functions
// outside xs. This function causes Logger instances to track if a record was
// written to the handler and facilitates EmitRequestCorrelationLogs behaviors.
func (loggerOpts) TrackWrites(b bool) LoggerOption {
	return func(cfg *loggerConfig) {
		cfg.trackWrites = b
	}
}

// Logger is an extended slog.Logger with context-aware logging methods
//
// We reserve the right to add additional methods to this interface
// in future releases. Authors are encouraged to embed
// a copy of this interface in their own projects to ensure
// continued compatibility with future versions.
type Logger interface {

	// Enabled returns true if the logger would emit a log for the supplied
	// level.
	Enabled(ctx context.Context, level slog.Level) bool

	// Debug emits a log record constructed from the given msg and attrs
	// arguments if the logger is enabled for that level.
	//
	// In performance critical applications where expensive attributes are
	// computed and passed in you would want to check that the target level is
	// enabled before constructing the attributes and call LogUnchecked instead
	// of this function.
	Debug(ctx context.Context, msg string, attrs ...slog.Attr)

	// Error emits a log record constructed from the given msg and attrs
	// arguments if the logger is enabled for that level.
	//
	// In performance critical applications where expensive attributes are
	// computed and passed in you would want to check that the target level is
	// enabled before constructing the attributes and call LogUnchecked instead
	// of this function.
	Error(ctx context.Context, msg string, attrs ...slog.Attr)

	// Info emits a log record constructed from the given msg and attrs
	// arguments if the logger is enabled for that level.
	//
	// In performance critical applications where expensive attributes are
	// computed and passed in you would want to check that the target level is
	// enabled before constructing the attributes and call LogUnchecked instead
	// of this function.
	Info(ctx context.Context, msg string, attrs ...slog.Attr)

	// Log emits a log record constructed from the given msg and attrs
	// arguments if the logger is enabled for that level.
	//
	// In performance critical applications where expensive attributes are
	// computed and passed in you would want to check that the target level is
	// enabled before constructing the attributes and call LogUnchecked instead
	// of this function.
	Log(ctx context.Context, level slog.Level, msg string, attrs ...slog.Attr)

	// LogUnchecked should largely be avoided except when operating in
	// performance critical code with an expensive to construct slice of attrs.
	// The caller should pre-check that the level for the log record they
	// intend to create is enabled, conditionally construct the attributes,
	// then call this function.
	//
	// Essentially LogUnchecked is similar to Log except filtering (such as log
	// level filtering) is disabled so such concerns go unchecked.
	//
	// The caller assumes the responsibility of filtering the log in the way they
	// prefer before calling this function.
	LogUnchecked(ctx context.Context, level slog.Level, msg string, attrs ...slog.Attr)

	// Warn emits a log record constructed from the given msg and attrs
	// arguments if the logger is enabled for that level.
	//
	// In performance critical applications where expensive attributes are
	// computed and passed in you would want to check that the target level is
	// enabled before constructing the attributes and call LogUnchecked instead
	// of this function.
	Warn(ctx context.Context, msg string, attrs ...slog.Attr)

	// extended functionality

	WithErr(ctx context.Context, err error) Logger
	WithAttrs(ctx context.Context, attrs ...slog.Attr) Logger
	SlogHandler(ctx context.Context) slog.Handler

	// Handle emits a given log record if the level of the record is enabled
	// for a given context.
	Handle(ctx context.Context, record slog.Record) error

	// SpanErr is the same as Error except it also records that an error happened
	// in the span which does not necessarily mean the span has failed.
	//
	// It is syntactic sugar that always calls xspan.RecordError.
	SpanErr(ctx context.Context, err error, msg string, attrs ...slog.Attr)

	// SpanFail is the same as Error except it also records that an error happened
	// in the span and that the span has failed.
	//
	// It is syntactic sugar that always calls xspan.Fail.
	SpanFail(ctx context.Context, err error, msg string, attrs ...slog.Attr)
}

type LoggerFactory interface {
	Enabled(context.Context, slog.Level) bool
	Logger(context.Context) Logger
}

func New(options ...LoggerOption) (Logger, error) {
	cfg := loggerConfig{
		level: defaultNewLoggerLevel,
	}

	for _, f := range options {
		f(&cfg)
	}

	if err := cfg.validate(); err != nil {
		return nil, errors.Join(ErrBadLoggerConfig, err)
	}

	// slog handler types:
	// - *structLoggerGrouped (wraps a *structLoggerGrouped or a slogStructLogger)
	// - slogStructLogger (wraps *structLogger or *structLoggerWriteTracked)

	// xslog logger types:
	// - *structLogger
	// - *structLoggerWriteTracked (always wraps a *structLogger)

	if cfg.levelSet && cfg.handlerSet {
		switch h := cfg.handler.(type) {
		case *structLoggerGrouped:
			// requires recursive reconstruction of the structLoggerGrouped which is not implemented and most likely will never be a wanted feature
			return nil, errors.Join(ErrBadLoggerConfig, errors.New("a structLoggerGrouped cannot be used in the option loggerOpts.Handler"))
		case slogStructLogger:
			// some cheap memory saving techniques given we know the exact internal composition of this handler type
			switch v := h.w.(type) {
			case *structLogger:
				if v.levelValid && v.level == cfg.level {
					if cfg.trackWrites {
						return &structLoggerWriteTracked{w: v}, nil
					}
					return v, nil
				}

				s := &structLogger{v.handler, cfg.level, true}
				if cfg.trackWrites {
					return &structLoggerWriteTracked{w: s}, nil
				}
				return s, nil
			case *structLoggerWriteTracked:
				if v.w.levelValid && v.w.level == cfg.level {
					return v, nil
				}

				handledRecPtr := v.handledRecPtr
				if handledRecPtr == nil {
					handledRecPtr = &v.handledRec
				}
				return &structLoggerWriteTracked{w: v.w, handledRecPtr: handledRecPtr}, nil
			default:
				panic(panicUnexpectedLoggerTypeInSlogStructLogger)
			}
		default:
		}
	}

	// If a handler was created just in time, it used the current value in
	// cfg.level for the logging level.
	//
	// So since it does no harm to set this to true, going to go ahead and set
	// it to true.
	//
	// There is no tracking of the level value within the handler itself and
	// this value DOES NOT represent the state of the handler's level value.
	//
	// I am setting this to true just to speed up comparisons since an if check
	// is faster than nesting function calls.
	levelValid := cfg.levelSet || !cfg.handlerSet

	s := &structLogger{cfg.handler, cfg.level, levelValid}
	if cfg.trackWrites {
		return &structLoggerWriteTracked{w: s}, nil
	}
	return s, nil
}
