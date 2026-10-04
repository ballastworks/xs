package xhttp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ballastworks/xs/xlog/xslog"
	"github.com/ballastworks/xs/xnet/xhttp"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// TestRequestLoggerAttrsWrittenOnce ensures request loggers do not repeat the
// source and trace attributes of a record. Decoding a log line into a map
// silently collapses duplicate keys, so they are counted in the raw output.
//
// Not parallel since it replaces the default logger factory.
func TestRequestLoggerAttrsWrittenOnce(t *testing.T) {
	op := xhttp.RequestLoggerFactoryOpts()

	tests := []struct {
		name string
		opts func(logf xslog.LoggerFactory) []xhttp.RequestLoggerFactoryOption
	}{
		{"no options", func(xslog.LoggerFactory) []xhttp.RequestLoggerFactoryOption {
			return nil
		}},
		{"LoggerFactory", func(logf xslog.LoggerFactory) []xhttp.RequestLoggerFactoryOption {
			return []xhttp.RequestLoggerFactoryOption{op.LoggerFactory(logf)}
		}},
		{"LoggerFactory and TrackWrites", func(logf xslog.LoggerFactory) []xhttp.RequestLoggerFactoryOption {
			return []xhttp.RequestLoggerFactoryOption{op.LoggerFactory(logf), op.TrackWrites(true)}
		}},
		{"TrackWrites", func(xslog.LoggerFactory) []xhttp.RequestLoggerFactoryOption {
			return []xhttp.RequestLoggerFactoryOption{op.TrackWrites(true)}
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer

			logger, err := xslog.New(xslog.LoggerOpts().Stream(&buf))
			require.NoError(t, err)

			// options without a LoggerFactory use the default logger factory
			old := xslog.SetDefaultFactory(xslog.StaticFactory(logger))
			t.Cleanup(func() {
				xslog.SetDefaultFactory(old)
			})

			logf, err := xhttp.NewRequestLoggerFactory(tc.opts(xslog.StaticFactory(logger))...)
			require.NoError(t, err)

			var h http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ctx := r.Context()
				logf.Logger(ctx).Info(ctx, "in handler")
			})
			h = xhttp.DefaultBeforeHandlerMiddlewareChain(logf).Wrap(h)
			{
				op := xhttp.DefaultTraceMiddlewareChainOpts()
				h = xhttp.EnrichedTraceMiddlewareChain(
					op.TracerProvider(newTestTracerProvider(t, sdktrace.AlwaysSample())),
					op.Propagators(propagation.TraceContext{}),
				).Wrap(h)
			}

			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", http.NoBody))

			lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
			require.NotEmpty(t, lines)

			for _, line := range lines {
				for _, key := range []string{
					"code.file.path", "code.function.name", "code.line.number",
					"trace_id", "trace_flags", "span_id",
				} {
					require.Equal(t, 1, bytes.Count(line, []byte(`"`+key+`":`)), "%s must be written once in: %s", key, line)
				}
			}
		})
	}
}

// groupedLogger is a Logger whose SlogHandler puts everything in a group, which
// xslog.New cannot wrap with write tracking.
type groupedLogger struct{ xslog.Logger }

func (g groupedLogger) SlogHandler(ctx context.Context) slog.Handler {
	return g.Logger.SlogHandler(ctx).WithGroup("app")
}

// TestRequestLoggerFactoryRejectsUnwrappableLogger ensures a LoggerFactory
// whose loggers cannot be wrapped with write tracking is rejected with an error
// by NewRequestLoggerFactory when TrackWrites needs to wrap them, rather than
// failing while handling a request.
func TestRequestLoggerFactoryRejectsUnwrappableLogger(t *testing.T) {
	op := xhttp.RequestLoggerFactoryOpts()

	logger, err := xslog.New(xslog.LoggerOpts().Stream(io.Discard))
	require.NoError(t, err)

	t.Run("TrackWrites", func(t *testing.T) {
		_, err := xhttp.NewRequestLoggerFactory(op.LoggerFactory(xslog.StaticFactory(groupedLogger{logger})), op.TrackWrites(true))
		require.ErrorIs(t, err, xhttp.ErrBadRequestLoggerFactoryConfig)
		require.ErrorIs(t, err, xslog.ErrBadLoggerConfig)
		require.ErrorContains(t, err, "WithGroup")

		_, err = xhttp.NewRequestLoggerFactory(op.LoggerFactory(xslog.StaticFactory(logger)), op.TrackWrites(true))
		require.NoError(t, err)
	})

	// without TrackWrites the factory's loggers are used as they are
	t.Run("no TrackWrites", func(t *testing.T) {
		_, err := xhttp.NewRequestLoggerFactory(op.LoggerFactory(xslog.StaticFactory(groupedLogger{logger})))
		require.NoError(t, err)
	})

	// Without a LoggerFactory option, the xslog default factory is read on
	// each request, so it cannot be checked when the factory is created.
	t.Run("default factory changed after creation panics with a readable message", func(t *testing.T) {
		old := xslog.SetDefaultFactory(xslog.StaticFactory(groupedLogger{logger}))
		t.Cleanup(func() {
			xslog.SetDefaultFactory(old)
		})

		logf, err := xhttp.NewRequestLoggerFactory(op.TrackWrites(true))
		require.NoError(t, err)

		var recovered any
		func() {
			defer func() { recovered = recover() }()
			serveWithRequestLogger(logf, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				ctx := r.Context()
				logf.Logger(ctx).Info(ctx, "m")
			}))
		}()

		require.NotNil(t, recovered)
		msg := fmt.Sprint(recovered)
		require.Contains(t, msg, "cannot add write tracking")
		require.Contains(t, msg, "WithGroup")
		require.NotContains(t, msg, "should be unreachable")
	})
}

// serveWithRequestLogger serves one request through the request logger middleware.
func serveWithRequestLogger(logf xslog.LoggerFactory, h http.Handler) {
	h = xhttp.DefaultBeforeHandlerMiddlewareChain(logf).Wrap(h)
	h = xhttp.MiddlewareLogger()(h)
	h = xhttp.MiddlewareRequestInFlightBegin()(h)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", http.NoBody))
}

// TestRequestLogHTTPRoute ensures request logs carry the matched route
// template as http.route once the router has routed the request, including
// logs from a logger cached before routing and the correlation log, and fall
// back to the request path when no route matched.
//
// Not parallel since it replaces the default xhttp logger factory.
func TestRequestLogHTTPRoute(t *testing.T) {
	for _, cached := range []bool{false, true} {
		t.Run(fmt.Sprintf("cached=%t", cached), func(t *testing.T) {
			var buf bytes.Buffer
			logger, err := xslog.New(xslog.LoggerOpts().Stream(&buf), xslog.LoggerOpts().Level(slog.LevelDebug))
			require.NoError(t, err)

			op := xhttp.RequestLoggerFactoryOpts()
			opts := []xhttp.RequestLoggerFactoryOption{op.LoggerFactory(xslog.StaticFactory(logger))}
			if cached {
				opts = append(opts, op.TrackWrites(true))
			}
			logf, err := xhttp.NewRequestLoggerFactory(opts...)
			require.NoError(t, err)

			old := xhttp.SetDefaultLoggerFactory(logf)
			t.Cleanup(func() {
				xhttp.SetDefaultLoggerFactory(old)
			})

			rt, err := xhttp.NewRouter(xhttp.RouterOpts().LoggerFactory(logf))
			require.NoError(t, err)
			rt.GetF("/items/{id}", func(w http.ResponseWriter, r *http.Request) {
				ctx := r.Context()
				logf.Logger(ctx).Info(ctx, "in handler")
			})

			serve := func(path string) []string {
				buf.Reset()

				var h http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					ctx := r.Context()
					logf.Logger(ctx).Info(ctx, "before routing")
					rt.ServeHTTP(w, r)
				})
				h = xhttp.DefaultBeforeHandlerMiddlewareChain(logf).Wrap(h)
				{
					op := xhttp.DefaultTraceMiddlewareChainOpts()
					h = xhttp.EnrichedTraceMiddlewareChain(
						op.TracerProvider(newTestTracerProvider(t, sdktrace.AlwaysSample())),
						op.Propagators(propagation.TraceContext{}),
					).Wrap(h)
				}
				h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, http.NoBody))

				lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
				for _, line := range lines {
					require.Equal(t, 1, strings.Count(line, `"http.route":`), "http.route must be written once in: %s", line)
				}
				return lines
			}

			routeOf := func(t *testing.T, lines []string, msg string) string {
				t.Helper()
				for _, line := range lines {
					if strings.Contains(line, `"msg":"`+msg+`"`) {
						var m map[string]any
						require.NoError(t, json.Unmarshal([]byte(line), &m))
						return m["http.route"].(string)
					}
				}
				t.Fatalf("no %q log in:\n%s", msg, strings.Join(lines, "\n"))
				return ""
			}

			lines := serve("/items/123")
			require.Equal(t, "/items/123", routeOf(t, lines, "before routing"), "before routing the route is not known yet")
			require.Equal(t, "/items/{id}", routeOf(t, lines, "in handler"))
			if cached {
				require.Equal(t, "/items/{id}", routeOf(t, lines, "request correlation"))
			}

			lines = serve("/missing")
			require.Equal(t, "/missing", routeOf(t, lines, "error http response"), "an unmatched request falls back to its path")
		})
	}
}

// TestRequestLoggerFactoryTrackWrites ensures TrackWrites decides whether a
// request gets a correlation log the same way with or without a LoggerFactory
// option.
//
// Not parallel since it replaces the default xslog logger factory.
func TestRequestLoggerFactoryTrackWrites(t *testing.T) {
	op := xhttp.RequestLoggerFactoryOpts()

	for _, withLoggerFactory := range []bool{false, true} {
		for _, trackWrites := range []bool{false, true} {
			t.Run(fmt.Sprintf("LoggerFactory=%t/TrackWrites=%t", withLoggerFactory, trackWrites), func(t *testing.T) {
				var buf bytes.Buffer
				logger, err := xslog.New(xslog.LoggerOpts().Stream(&buf))
				require.NoError(t, err)

				old := xslog.SetDefaultFactory(xslog.StaticFactory(logger))
				t.Cleanup(func() {
					xslog.SetDefaultFactory(old)
				})

				opts := []xhttp.RequestLoggerFactoryOption{op.CacheLogger(true), op.TrackWrites(trackWrites)}
				if withLoggerFactory {
					opts = append(opts, op.LoggerFactory(xslog.StaticFactory(logger)))
				}
				logf, err := xhttp.NewRequestLoggerFactory(opts...)
				require.NoError(t, err)

				var h http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					ctx := r.Context()
					logf.Logger(ctx).Info(ctx, "in handler")
				})
				h = xhttp.DefaultBeforeHandlerMiddlewareChain(logf).Wrap(h)
				{
					op := xhttp.DefaultTraceMiddlewareChainOpts()
					h = xhttp.EnrichedTraceMiddlewareChain(
						op.TracerProvider(newTestTracerProvider(t, sdktrace.AlwaysSample())),
						op.Propagators(propagation.TraceContext{}),
					).Wrap(h)
				}
				h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", http.NoBody))

				require.Contains(t, buf.String(), `"msg":"in handler"`)
				require.Equal(t, trackWrites, strings.Contains(buf.String(), `"msg":"request correlation"`))
			})
		}
	}
}
