package xslog_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"testing/slogtest"

	"github.com/ballastworks/xs/xlog/xslog"
	"github.com/stretchr/testify/require"
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
