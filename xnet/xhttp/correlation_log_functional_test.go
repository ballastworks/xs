package xhttp_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ballastworks/xs/xlog/xslog"
	"github.com/ballastworks/xs/xnet/xhttp"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

const correlationLogMsg = "request correlation"

func newTestTracerProvider(t *testing.T, sampler sdktrace.Sampler) *sdktrace.TracerProvider {
	t.Helper()

	tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sampler))

	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
	})

	return tp
}

func decodeLogLines(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()

	var result []map[string]any

	sc := bufio.NewScanner(buf)
	for sc.Scan() {
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}

		var m map[string]any
		require.NoError(t, json.Unmarshal(sc.Bytes(), &m))
		result = append(result, m)
	}
	require.NoError(t, sc.Err())

	return result
}

func TestRequestCorrelationLogTraceInfo(t *testing.T) {
	const remoteTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	const remoteTraceparent = "00-" + remoteTraceID + "-00f067aa0ba902b7-01"

	type handlerFunc = func(t *testing.T, logf xslog.LoggerFactory, r *http.Request)

	logOnce := func(t *testing.T, logf xslog.LoggerFactory, r *http.Request) {
		ctx := r.Context()
		logf.Logger(ctx).Info(ctx, "in handler")
	}

	tests := []struct {
		name        string
		sampler     sdktrace.Sampler
		traceparent string
		handler     handlerFunc
		expFlags    string
		expTraceID  string
		// expHandlerLogs is the number of logs the handler emits before the
		// correlation log; zero means no correlation log is expected at all
		expHandlerLogs int
	}{
		{
			name:           "handler logs in the request span",
			sampler:        sdktrace.AlwaysSample(),
			handler:        logOnce,
			expFlags:       "01",
			expHandlerLogs: 1,
		},
		{
			name:    "handler logs only in a child span",
			sampler: sdktrace.AlwaysSample(),
			handler: func(t *testing.T, logf xslog.LoggerFactory, r *http.Request) {
				// using the provider of the request span since the otel
				// globals are intentionally not set by this test
				tp := trace.SpanFromContext(r.Context()).TracerProvider()

				ctx, span := tp.Tracer("test").Start(r.Context(), "child")
				defer span.End()

				require.NotEqual(t,
					trace.SpanContextFromContext(r.Context()).SpanID(),
					trace.SpanContextFromContext(ctx).SpanID(),
				)

				logf.Logger(ctx).Warn(ctx, "in child span")
			},
			expFlags:       "01",
			expHandlerLogs: 1,
		},
		{
			name:    "handler logs many times",
			sampler: sdktrace.AlwaysSample(),
			handler: func(t *testing.T, logf xslog.LoggerFactory, r *http.Request) {
				logOnce(t, logf, r)
				logOnce(t, logf, r)
				logOnce(t, logf, r)
			},
			expFlags:       "01",
			expHandlerLogs: 3,
		},
		{
			name:           "span is not sampled",
			sampler:        sdktrace.NeverSample(),
			handler:        logOnce,
			expFlags:       "00",
			expHandlerLogs: 1,
		},
		{
			name:           "trace continues from a remote parent",
			sampler:        sdktrace.AlwaysSample(),
			traceparent:    remoteTraceparent,
			handler:        logOnce,
			expFlags:       "01",
			expTraceID:     remoteTraceID,
			expHandlerLogs: 1,
		},
		{
			name:    "handler logs then panics",
			sampler: sdktrace.AlwaysSample(),
			handler: func(t *testing.T, logf xslog.LoggerFactory, r *http.Request) {
				logOnce(t, logf, r)
				panic(http.ErrAbortHandler)
			},
			expFlags:       "01",
			expHandlerLogs: 1,
		},
		{
			name:           "handler never logs so there is no correlation log",
			sampler:        sdktrace.AlwaysSample(),
			handler:        func(*testing.T, xslog.LoggerFactory, *http.Request) {},
			expHandlerLogs: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tp := newTestTracerProvider(t, tc.sampler)

			var buf bytes.Buffer

			logger, err := xslog.New(xslog.LoggerOpts().Stream(&buf))
			require.NoError(t, err)

			op := xhttp.RequestLoggerFactoryOpts()
			logf, err := xhttp.NewRequestLoggerFactory(
				op.LoggerFactory(xslog.StaticFactory(logger)),
				op.TrackWrites(true),
			)
			require.NoError(t, err)

			var reqSpanCtx trace.SpanContext

			var h http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reqSpanCtx = trace.SpanContextFromContext(r.Context())
				tc.handler(t, logf, r)
			})
			h = xhttp.DefaultBeforeHandlerMiddlewareChain(logf).Wrap(h)
			{
				op := xhttp.DefaultTraceMiddlewareChainOpts()
				h = xhttp.EnrichedTraceMiddlewareChain(
					op.TracerProvider(tp),
					op.Propagators(propagation.TraceContext{}),
				).Wrap(h)
			}

			req := httptest.NewRequest(http.MethodGet, "/x", http.NoBody)
			if tc.traceparent != "" {
				req.Header.Set("Traceparent", tc.traceparent)
			}

			func() {
				defer func() {
					if r := recover(); r != nil && r != http.ErrAbortHandler {
						panic(r)
					}
				}()

				h.ServeHTTP(httptest.NewRecorder(), req)
			}()

			require.True(t, reqSpanCtx.IsValid())
			if tc.expTraceID != "" {
				require.Equal(t, tc.expTraceID, reqSpanCtx.TraceID().String())
			}

			logs := decodeLogLines(t, &buf)

			if tc.expHandlerLogs == 0 {
				require.Empty(t, logs)
				return
			}

			require.Len(t, logs, tc.expHandlerLogs+1)

			// every log in the request shares the trace
			for _, l := range logs {
				require.Equal(t, reqSpanCtx.TraceID().String(), l["trace_id"], "msg=%v", l["msg"])
				require.Equal(t, tc.expFlags, l["trace_flags"], "msg=%v", l["msg"])
				require.NotEmpty(t, l["span_id"], "msg=%v", l["msg"])
			}

			// the correlation log is last and always identifies the request
			// span regardless of which span the handler logged from
			cl := logs[len(logs)-1]
			require.Equal(t, correlationLogMsg, cl["msg"])
			require.Equal(t, reqSpanCtx.SpanID().String(), cl["span_id"])

			for _, l := range logs[:len(logs)-1] {
				require.NotEqual(t, correlationLogMsg, l["msg"])
			}
		})
	}
}
