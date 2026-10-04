package xslog_test

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"testing"

	"github.com/ballastworks/xs/xlog/xslog"
	"github.com/stretchr/testify/require"
)

// TestNewFromSlogHandler ensures a logger created from the SlogHandler of
// another logger without a level reuses that logger rather than wrapping it,
// which would write the source attributes of every record twice.
func TestNewFromSlogHandler(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name             string
		innerTrackWrites bool
		trackWrites      bool
	}{
		{"plain", false, false},
		{"track writes", false, true},
		{"wrapped logger tracks writes", true, false},
		{"both track writes", true, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			inner, err := xslog.New(
				xslog.LoggerOpts().Stream(&buf),
				xslog.LoggerOpts().Level(slog.LevelWarn),
				xslog.LoggerOpts().TrackWrites(tc.innerTrackWrites),
			)
			require.NoError(t, err)

			logger, err := xslog.New(
				xslog.LoggerOpts().Handler(inner.SlogHandler(ctx)),
				xslog.LoggerOpts().TrackWrites(tc.trackWrites),
			)
			require.NoError(t, err)

			require.False(t, logger.Enabled(ctx, slog.LevelInfo), "the wrapped logger's level must be kept")

			tracked := tc.innerTrackWrites || tc.trackWrites
			if tracked {
				require.False(t, xslog.RecordWritten(logger))
			}

			logger.Warn(ctx, "msg")

			for _, key := range []string{"code.file.path", "code.function.name", "code.line.number"} {
				require.Equal(t, 1, bytes.Count(buf.Bytes(), []byte(`"`+key+`":`)), "%s must be written once", key)
			}

			if tracked {
				require.True(t, xslog.RecordWritten(logger))
			}
		})
	}
}

// TestNewFromSlogHandlerLevel ensures a level set alongside the SlogHandler of
// another logger is applied whether or not that logger tracks writes, and that
// the new logger shares the write tracking of a logger that does.
func TestNewFromSlogHandlerLevel(t *testing.T) {
	ctx := context.Background()

	for _, innerTrackWrites := range []bool{false, true} {
		for _, level := range []slog.Level{slog.LevelDebug, slog.LevelError} {
			t.Run(fmt.Sprintf("innerTrackWrites=%t/level=%s", innerTrackWrites, level), func(t *testing.T) {
				var buf bytes.Buffer
				inner, err := xslog.New(
					xslog.LoggerOpts().Stream(&buf),
					xslog.LoggerOpts().Level(slog.LevelWarn),
					xslog.LoggerOpts().TrackWrites(innerTrackWrites),
				)
				require.NoError(t, err)

				logger, err := xslog.New(
					xslog.LoggerOpts().Handler(inner.SlogHandler(ctx)),
					xslog.LoggerOpts().Level(level),
				)
				require.NoError(t, err)

				require.True(t, logger.Enabled(ctx, level), "the new level must be applied")
				require.False(t, logger.Enabled(ctx, level-1), "the new level must be applied")
				require.False(t, inner.Enabled(ctx, slog.LevelInfo), "the wrapped logger's level must not change")

				logger.Log(ctx, level, "msg")

				require.Equal(t, 1, bytes.Count(buf.Bytes(), []byte(`"msg":"msg"`)), "a record at the new level must be written")

				if innerTrackWrites {
					require.True(t, xslog.RecordWritten(logger))
					require.True(t, xslog.RecordWritten(inner), "the new logger must share the wrapped logger's write tracking")
				}
			})
		}
	}
}
