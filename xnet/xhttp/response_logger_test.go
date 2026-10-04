package xhttp_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ballastworks/xs/xlog/xslog"
	"github.com/ballastworks/xs/xnet/xhttp"
	"github.com/stretchr/testify/require"
)

// TestResponseLoggerFactoryResolution ensures error responses log through the
// most specific configured logger factory, in the same order whether they are
// rendered with WriteResp or a StaticHandler: the response's own, then its
// response factory's, then the one in the request context, then the xhttp
// default. Responses the router renders itself use the router's factory as
// the context factory when one was configured.
//
// Not parallel since it replaces the default xhttp logger factory.
func TestResponseLoggerFactoryResolution(t *testing.T) {
	const logged = "error http response"

	newSink := func(t *testing.T) (*bytes.Buffer, xslog.LoggerFactory) {
		t.Helper()

		var buf bytes.Buffer
		logger, err := xslog.New(
			xslog.LoggerOpts().Stream(&buf),
			xslog.LoggerOpts().Level(slog.LevelDebug),
		)
		require.NoError(t, err)

		return &buf, xslog.StaticFactory(logger)
	}

	newRespFactory := func(t *testing.T, logf xslog.LoggerFactory) *xhttp.ResponseFactory {
		t.Helper()

		rf, err := xhttp.NewResponseFactory(xhttp.ResponseFactoryOpts().LoggerFactory(logf))
		require.NoError(t, err)

		return rf
	}

	serveStatic := func(ctx context.Context, h http.Handler) {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", http.NoBody).WithContext(ctx))
	}

	// serveRouter serves path through a router built with opts, inside the
	// standard middleware and with ctxLogf in the request context when set,
	// as a Server would add it.
	serveRouter := func(t *testing.T, ctxLogf xslog.LoggerFactory, path string, opts ...xhttp.RouterOption) {
		t.Helper()

		rt, err := xhttp.NewRouter(opts...)
		require.NoError(t, err)

		rt.GetE("/error", func(w http.ResponseWriter, r *http.Request) error {
			return errors.New("handler failed")
		})
		rt.GetE("/err-resp", func(w http.ResponseWriter, r *http.Request) error {
			return xhttp.NewErrResp(xhttp.ErrRespOpts().StatusCode(http.StatusConflict))
		})
		rt.GetF("/panic", func(w http.ResponseWriter, r *http.Request) {
			panic(errors.New("handler panicked"))
		})

		_, nopLogf := newSink(t)
		h := xhttp.MiddlewareChainExt(xhttp.DefaultBeforeHandlerMiddlewareChain(nopLogf)).Handler(rt)
		if ctxLogf != nil {
			h = xhttp.MiddlewareAddLoggerFactoryToContext(ctxLogf)(h)
		}

		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, http.NoBody))
	}

	ctxWith := func(logf xslog.LoggerFactory) context.Context {
		var ctx context.Context
		xhttp.MiddlewareAddLoggerFactoryToContext(logf)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx = r.Context()
		})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", http.NoBody))

		return ctx
	}

	tests := []struct {
		name string
		// run renders an error response and returns the sink that must
		// receive its log and the other sinks in play, which must not
		run func(t *testing.T) (want *bytes.Buffer, others []*bytes.Buffer)
	}{
		{"WriteResp uses the response factory's LoggerFactory", func(t *testing.T) (*bytes.Buffer, []*bytes.Buffer) {
			rfBuf, rfLogf := newSink(t)
			newRespFactory(t, rfLogf).NewInternalErr(errors.New("boom")).WriteResp(context.Background(), httptest.NewRecorder())
			return rfBuf, nil
		}},
		{"StaticHandler uses the response factory's LoggerFactory", func(t *testing.T) (*bytes.Buffer, []*bytes.Buffer) {
			rfBuf, rfLogf := newSink(t)
			serveStatic(context.Background(), newRespFactory(t, rfLogf).NewErr().StaticHandler())
			return rfBuf, nil
		}},
		{"WriteResp uses the response's LoggerFactory", func(t *testing.T) (*bytes.Buffer, []*bytes.Buffer) {
			respBuf, respLogf := newSink(t)
			xhttp.NewErrResp(xhttp.ErrRespOpts().LoggerFactory(respLogf)).WriteResp(context.Background(), httptest.NewRecorder())
			return respBuf, nil
		}},
		{"StaticHandler uses the response's LoggerFactory", func(t *testing.T) (*bytes.Buffer, []*bytes.Buffer) {
			respBuf, respLogf := newSink(t)
			serveStatic(context.Background(), xhttp.NewErrResp(xhttp.ErrRespOpts().LoggerFactory(respLogf)).StaticHandler())
			return respBuf, nil
		}},
		{"the response's LoggerFactory takes precedence over its response factory's", func(t *testing.T) (*bytes.Buffer, []*bytes.Buffer) {
			respBuf, respLogf := newSink(t)
			rfBuf, rfLogf := newSink(t)
			rf := newRespFactory(t, rfLogf)
			rf.NewErr(xhttp.ErrRespOpts().LoggerFactory(respLogf)).WriteResp(context.Background(), httptest.NewRecorder())
			serveStatic(context.Background(), rf.NewErr(xhttp.ErrRespOpts().LoggerFactory(respLogf)).StaticHandler())
			return respBuf, []*bytes.Buffer{rfBuf}
		}},
		{"the response factory's LoggerFactory takes precedence over the context's", func(t *testing.T) (*bytes.Buffer, []*bytes.Buffer) {
			rfBuf, rfLogf := newSink(t)
			ctxBuf, ctxLogf := newSink(t)
			rf := newRespFactory(t, rfLogf)
			ctx := ctxWith(ctxLogf)
			rf.NewErr().WriteResp(ctx, httptest.NewRecorder())
			serveStatic(ctx, rf.NewErr().StaticHandler())
			return rfBuf, []*bytes.Buffer{ctxBuf}
		}},
		{"WriteResp uses the context's logger factory when none is configured", func(t *testing.T) (*bytes.Buffer, []*bytes.Buffer) {
			ctxBuf, ctxLogf := newSink(t)
			xhttp.NewErrResp().WriteResp(ctxWith(ctxLogf), httptest.NewRecorder())
			return ctxBuf, nil
		}},
		{"StaticHandler uses the context's logger factory when none is configured", func(t *testing.T) (*bytes.Buffer, []*bytes.Buffer) {
			ctxBuf, ctxLogf := newSink(t)
			serveStatic(ctxWith(ctxLogf), xhttp.NewErrResp().StaticHandler())
			return ctxBuf, nil
		}},
		{"router renders a handler error with its LoggerFactory", func(t *testing.T) (*bytes.Buffer, []*bytes.Buffer) {
			rtBuf, rtLogf := newSink(t)
			ctxBuf, ctxLogf := newSink(t)
			serveRouter(t, ctxLogf, "/error", xhttp.RouterOpts().LoggerFactory(rtLogf))
			return rtBuf, []*bytes.Buffer{ctxBuf}
		}},
		{"router renders a returned error response with its LoggerFactory", func(t *testing.T) (*bytes.Buffer, []*bytes.Buffer) {
			rtBuf, rtLogf := newSink(t)
			ctxBuf, ctxLogf := newSink(t)
			serveRouter(t, ctxLogf, "/err-resp", xhttp.RouterOpts().LoggerFactory(rtLogf))
			return rtBuf, []*bytes.Buffer{ctxBuf}
		}},
		{"router renders a panic with its LoggerFactory", func(t *testing.T) (*bytes.Buffer, []*bytes.Buffer) {
			rtBuf, rtLogf := newSink(t)
			ctxBuf, ctxLogf := newSink(t)
			serveRouter(t, ctxLogf, "/panic", xhttp.RouterOpts().LoggerFactory(rtLogf))
			return rtBuf, []*bytes.Buffer{ctxBuf}
		}},
		{"router renders not found with its LoggerFactory", func(t *testing.T) (*bytes.Buffer, []*bytes.Buffer) {
			rtBuf, rtLogf := newSink(t)
			ctxBuf, ctxLogf := newSink(t)
			serveRouter(t, ctxLogf, "/missing", xhttp.RouterOpts().LoggerFactory(rtLogf))
			return rtBuf, []*bytes.Buffer{ctxBuf}
		}},
		{"router without a LoggerFactory renders through the context's", func(t *testing.T) (*bytes.Buffer, []*bytes.Buffer) {
			ctxBuf, ctxLogf := newSink(t)
			serveRouter(t, ctxLogf, "/error")
			return ctxBuf, nil
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			defBuf, defLogf := newSink(t)
			old := xhttp.SetDefaultLoggerFactory(defLogf)
			t.Cleanup(func() {
				xhttp.SetDefaultLoggerFactory(old)
			})

			want, others := tc.run(t)

			require.Contains(t, want.String(), logged, "the configured logger factory must log the response")
			for _, buf := range append(others, defBuf) {
				require.NotContains(t, buf.String(), logged, "only the most specific logger factory may log the response")
			}
		})
	}

	t.Run("falls back to the xhttp default", func(t *testing.T) {
		defBuf, defLogf := newSink(t)
		old := xhttp.SetDefaultLoggerFactory(defLogf)
		t.Cleanup(func() {
			xhttp.SetDefaultLoggerFactory(old)
		})

		xhttp.NewErrResp().WriteResp(context.Background(), httptest.NewRecorder())
		serveStatic(context.Background(), xhttp.NewErrResp().StaticHandler())

		require.Equal(t, 2, strings.Count(defBuf.String(), logged))
	})
}
