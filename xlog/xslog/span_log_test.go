package xslog_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"testing"

	"github.com/ballastworks/xs/xerrors"
	"github.com/ballastworks/xs/xlog/xncslog"
	"github.com/ballastworks/xs/xlog/xslog"
	"github.com/stretchr/testify/require"
)

type spanLogFunc = func(ctx context.Context, logger xslog.Logger, err error, msg string, attr slog.Attr)

// loggerAttr is added to every logger made by newDefaultTestLogger via
// WithAttrs, like the request scoped attributes of a request logger.
var loggerAttr = slog.String("a", "1")

// newDefaultTestLogger returns a logger with loggerAttr writing JSON to the
// returned buffer and installs it as the default logger factory for the
// duration of the test.
//
// Tests using this must not run in parallel since the default factory is
// global state.
func newDefaultTestLogger(t *testing.T, trackWrites bool) (xslog.Logger, *bytes.Buffer) {
	t.Helper()

	var buf bytes.Buffer
	logger, err := xslog.New(
		xslog.LoggerOpts().Stream(&buf),
		xslog.LoggerOpts().TrackWrites(trackWrites),
	)
	require.NoError(t, err)

	logger = logger.WithAttrs(context.Background(), loggerAttr)

	old := xslog.SetDefaultFactory(xslog.StaticFactory(logger))
	t.Cleanup(func() {
		xslog.SetDefaultFactory(old)
	})

	return logger, &buf
}

func decodeSingleLogLine(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()

	var m map[string]any
	dec := json.NewDecoder(buf)
	require.NoError(t, dec.Decode(&m))
	require.False(t, dec.More(), "expected exactly one log record")

	return m
}

// attrKeys returns the top level keys of a single JSON log line that follow
// the msg key, in the order they were written.
func attrKeys(t *testing.T, line []byte) []string {
	t.Helper()

	dec := json.NewDecoder(bytes.NewReader(line))

	tok, err := dec.Token()
	require.NoError(t, err)
	require.Equal(t, json.Delim('{'), tok)

	var keys []string
	var afterMsg bool
	for dec.More() {
		tok, err := dec.Token()
		require.NoError(t, err)

		key := tok.(string)
		if afterMsg {
			keys = append(keys, key)
		}
		if key == slog.MessageKey {
			afterMsg = true
		}

		var skip json.RawMessage
		require.NoError(t, dec.Decode(&skip))
	}

	return keys
}

func TestSpanLog(t *testing.T) {
	tests := []struct {
		name        string
		trackWrites bool
		log         spanLogFunc
	}{
		{"xslog.SpanErr", false, func(ctx context.Context, _ xslog.Logger, err error, msg string, attr slog.Attr) {
			xslog.SpanErr(ctx, err, msg, attr)
		}},
		{"xslog.SpanFail", false, func(ctx context.Context, _ xslog.Logger, err error, msg string, attr slog.Attr) {
			xslog.SpanFail(ctx, err, msg, attr)
		}},
		{"xncslog.SpanErr", false, func(ctx context.Context, _ xslog.Logger, err error, msg string, attr slog.Attr) {
			xncslog.SpanErr(ctx, err, msg, attr)
		}},
		{"xncslog.SpanFail", false, func(ctx context.Context, _ xslog.Logger, err error, msg string, attr slog.Attr) {
			xncslog.SpanFail(ctx, err, msg, attr)
		}},
		{"Logger.SpanErr", false, func(ctx context.Context, logger xslog.Logger, err error, msg string, attr slog.Attr) {
			logger.SpanErr(ctx, err, msg, attr)
		}},
		{"Logger.SpanFail", false, func(ctx context.Context, logger xslog.Logger, err error, msg string, attr slog.Attr) {
			logger.SpanFail(ctx, err, msg, attr)
		}},
		{"write tracked Logger.SpanErr", true, func(ctx context.Context, logger xslog.Logger, err error, msg string, attr slog.Attr) {
			logger.SpanErr(ctx, err, msg, attr)
		}},
		{"write tracked Logger.SpanFail", true, func(ctx context.Context, logger xslog.Logger, err error, msg string, attr slog.Attr) {
			logger.SpanFail(ctx, err, msg, attr)
		}},
		{"LoggerWrappingFactory.SpanErr", false, func(ctx context.Context, logger xslog.Logger, err error, msg string, attr slog.Attr) {
			xslog.NewLoggerWrappingFactory(xslog.StaticFactory(logger)).SpanErr(ctx, err, msg, attr)
		}},
		{"LoggerWrappingFactory.SpanFail", false, func(ctx context.Context, logger xslog.Logger, err error, msg string, attr slog.Attr) {
			xslog.NewLoggerWrappingFactory(xslog.StaticFactory(logger)).SpanFail(ctx, err, msg, attr)
		}},
		{"LoggerWrappingContextFactory.SpanErr", false, func(ctx context.Context, logger xslog.Logger, err error, msg string, attr slog.Attr) {
			xslog.NewLoggerWrappingContextFactory(ctx, xslog.StaticFactory(logger)).SpanErr(err, msg, attr)
		}},
		{"LoggerWrappingContextFactory.SpanFail", false, func(ctx context.Context, logger xslog.Logger, err error, msg string, attr slog.Attr) {
			xslog.NewLoggerWrappingContextFactory(ctx, xslog.StaticFactory(logger)).SpanFail(err, msg, attr)
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for _, withErr := range []bool{true, false} {
				var err error
				if withErr {
					err = xerrors.New("boom")
				}

				logger, buf := newDefaultTestLogger(t, tc.trackWrites)

				tc.log(context.Background(), logger, err, "span log msg", slog.String("k", "v"))

				keys := attrKeys(t, buf.Bytes())

				m := decodeSingleLogLine(t, buf)
				require.Equal(t, "ERROR", m["level"])
				require.Equal(t, "span log msg", m["msg"])
				require.Equal(t, "v", m["k"], "attrs passed to %s must be in the log record", tc.name)
				require.Contains(t, m["code.function.name"], "TestSpanLog", "source must be the caller of %s", tc.name)

				if !withErr {
					require.NotContains(t, m, xslog.ErrorLogKey, "a nil err must not be in the log record")
					require.NotContains(t, m, xslog.StacktraceLogKey)
					require.Equal(t, []string{"a", "k"}, keys[:2])
					continue
				}

				require.Equal(t, "boom", m[xslog.ErrorLogKey], "a non-nil err passed to %s must be in the log record", tc.name)
				require.NotEmpty(t, m[xslog.StacktraceLogKey], "a traced err passed to %s must have its stacktrace in the log record", tc.name)
				require.Equal(t, []string{"a", xslog.ErrorLogKey, xslog.StacktraceLogKey, "k"}, keys[:4], "the err passed to %s must come after the logger's attributes and before attrs", tc.name)
			}
		})
	}
}

// TestSpanLogNilErrWithErr ensures a nil err passed to SpanErr or SpanFail on a
// logger derived from WithErr adds no error attributes of its own, leaving
// exactly the ones from WithErr.
func TestSpanLogNilErrWithErr(t *testing.T) {
	tests := []struct {
		name        string
		trackWrites bool
		log         func(ctx context.Context, logger xslog.Logger, msg string)
	}{
		{"Logger.SpanErr", false, func(ctx context.Context, logger xslog.Logger, msg string) {
			logger.SpanErr(ctx, nil, msg)
		}},
		{"Logger.SpanFail", false, func(ctx context.Context, logger xslog.Logger, msg string) {
			logger.SpanFail(ctx, nil, msg)
		}},
		{"write tracked Logger.SpanErr", true, func(ctx context.Context, logger xslog.Logger, msg string) {
			logger.SpanErr(ctx, nil, msg)
		}},
		{"write tracked Logger.SpanFail", true, func(ctx context.Context, logger xslog.Logger, msg string) {
			logger.SpanFail(ctx, nil, msg)
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			logger, buf := newDefaultTestLogger(t, tc.trackWrites)

			tc.log(ctx, logger.WithErr(ctx, xerrors.New("boom")), "span log msg")

			// decoding into a map silently collapses duplicate keys, so count them in the raw output
			require.Equal(t, 1, bytes.Count(buf.Bytes(), []byte(`"`+xslog.ErrorLogKey+`":`)))
			require.Equal(t, 1, bytes.Count(buf.Bytes(), []byte(`"`+xslog.StacktraceLogKey+`":`)))
			require.Equal(t, []string{"a", xslog.ErrorLogKey, xslog.StacktraceLogKey}, attrKeys(t, buf.Bytes())[:3])

			m := decodeSingleLogLine(t, buf)
			require.Equal(t, "boom", m[xslog.ErrorLogKey])
		})
	}
}

// TestWithErrOrder ensures WithErr places the error attributes after the
// attributes the logger already has and before any added later.
func TestWithErrOrder(t *testing.T) {
	for _, trackWrites := range []bool{false, true} {
		t.Run(fmt.Sprintf("trackWrites=%t", trackWrites), func(t *testing.T) {
			ctx := context.Background()
			logger, buf := newDefaultTestLogger(t, trackWrites)

			logger.WithErr(ctx, xerrors.New("boom")).WithAttrs(ctx, slog.String("b", "2")).Info(ctx, "msg", slog.String("k", "v"))

			require.Equal(t, []string{"a", xslog.ErrorLogKey, xslog.StacktraceLogKey, "b", "k"}, attrKeys(t, buf.Bytes())[:5])

			if trackWrites {
				require.True(t, xslog.RecordWritten(logger), "loggers derived via WithErr must share write tracking")
			}
		})
	}
}
