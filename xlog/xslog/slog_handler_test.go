package xslog_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"log/slog"
	"runtime"
	"testing"
	"testing/slogtest"
	"time"

	"github.com/ballastworks/xs/xlog/xslog"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
)

// TestSlogHandler runs the standard library slog.Handler conformance suite
// against the handler returned by SlogHandler.
func TestSlogHandler(t *testing.T) {
	for _, trackWrites := range []bool{false, true} {
		t.Run(fmt.Sprintf("trackWrites=%t", trackWrites), func(t *testing.T) {
			// slogtest.Run runs its cases sequentially, so they can share buf
			var buf bytes.Buffer

			slogtest.Run(t,
				func(t *testing.T) slog.Handler {
					buf.Reset()

					logger, err := xslog.New(
						xslog.LoggerOpts().Stream(&buf),
						xslog.LoggerOpts().TrackWrites(trackWrites),
					)
					require.NoError(t, err)

					return logger.SlogHandler(context.Background())
				},
				func(t *testing.T) map[string]any {
					var m map[string]any
					require.NoError(t, json.Unmarshal(buf.Bytes(), &m))

					return m
				},
			)
		})
	}
}

// TestSlogHandlerNestedGroupsTrackWrites ensures records logged through nested
// groups still reach the write tracking of the logger the groups came from.
func TestSlogHandlerNestedGroupsTrackWrites(t *testing.T) {
	logger, err := xslog.New(
		xslog.LoggerOpts().Stream(io.Discard),
		xslog.LoggerOpts().TrackWrites(true),
	)
	require.NoError(t, err)

	h := logger.SlogHandler(context.Background()).
		WithGroup("a").
		WithAttrs([]slog.Attr{slog.String("x", "1")}).
		WithGroup("b")

	require.False(t, xslog.RecordWritten(h))

	slog.New(h).Info("msg")

	require.True(t, xslog.RecordWritten(h))
	require.True(t, xslog.RecordWritten(logger))
}

// TestSlogHandlerZeroPC ensures a record without a PC gets no code.* source
// attributes, as the log/slog Handler contract requires, while keeping its
// trace attributes. testing/slogtest only checks the standard source key, so
// it cannot catch this for xslog's code.* keys.
func TestSlogHandlerZeroPC(t *testing.T) {
	sourceKeys := []string{`"code.file.path":`, `"code.function.name":`, `"code.line.number":`}
	traceKeys := []string{`"trace_id":`, `"trace_flags":`, `"span_id":`}

	spanCtx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{1},
		SpanID:     trace.SpanID{1},
		TraceFlags: trace.FlagsSampled,
	}))

	var pcs [1]uintptr
	runtime.Callers(1, pcs[:])

	tests := []struct {
		name      string
		ctx       context.Context
		pc        uintptr
		expSource bool
		expTrace  bool
	}{
		{"zero PC", context.Background(), 0, false, false},
		{"zero PC in a span", spanCtx, 0, false, true},
		{"caller PC", context.Background(), pcs[0], true, false},
		{"caller PC in a span", spanCtx, pcs[0], true, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			logger, err := xslog.New(xslog.LoggerOpts().Stream(&buf))
			require.NoError(t, err)

			require.NoError(t, logger.SlogHandler(tc.ctx).Handle(tc.ctx, slog.NewRecord(time.Now(), slog.LevelInfo, "msg", tc.pc)))

			for _, key := range sourceKeys {
				require.Equal(t, tc.expSource, bytes.Contains(buf.Bytes(), []byte(key)), "source key %s in: %s", key, buf.String())
			}
			for _, key := range traceKeys {
				require.Equal(t, tc.expTrace, bytes.Contains(buf.Bytes(), []byte(key)), "trace key %s in: %s", key, buf.String())
			}
		})
	}

	// slog.SetDefault bridges the standard log package to the handler without
	// capturing a PC unless the log flags request file information.
	t.Run("standard log package bridged by slog.SetDefault", func(t *testing.T) {
		var buf bytes.Buffer
		logger, err := xslog.New(xslog.LoggerOpts().Stream(&buf))
		require.NoError(t, err)

		oldDefault, oldOutput, oldFlags := slog.Default(), log.Writer(), log.Flags()
		t.Cleanup(func() {
			slog.SetDefault(oldDefault)
			log.SetOutput(oldOutput)
			log.SetFlags(oldFlags)
		})

		log.SetFlags(log.LstdFlags)
		slog.SetDefault(slog.New(logger.SlogHandler(context.Background())))

		log.Printf("from the standard log package")

		require.Contains(t, buf.String(), `"msg":"from the standard log package"`)
		for _, key := range sourceKeys {
			require.NotContains(t, buf.String(), key)
		}
	})
}
