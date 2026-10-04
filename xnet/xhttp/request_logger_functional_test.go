package xhttp_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
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
