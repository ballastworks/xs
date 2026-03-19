package xhttp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/ballastworks/xs/xcontext/xspan"
	"github.com/ballastworks/xs/xerrors"
	"github.com/ballastworks/xs/xlog/xslog"

	"github.com/julienschmidt/httprouter"
)

var (
	ErrHandlerPanicUnknownCause = errors.New("panic in handler from unknown cause")
	ErrBadRouterConfig          = errors.New("bad router config")

	errRespGatewayTimeout = NewErrResp(http.StatusGatewayTimeout, "gateway-timeout", "likely encountered a timeout with an upstream service or an internal client")
)

type errHandlerFunc = func(http.ResponseWriter, *http.Request) error

type Router interface {
	// Router is exported because it is used as an argument type for the routerOpts.Router option.

	Handler(method, path string, handler http.Handler)
	ServeHTTP(w http.ResponseWriter, r *http.Request)
}

func newDefaultRouter() *httprouter.Router {
	r := httprouter.New()

	r.NotFound = routerNotFoundHandler
	r.MethodNotAllowed = routerMethodNotAllowedHandler
	r.RedirectTrailingSlash = false
	r.RedirectFixedPath = false

	return r
}

type router struct {
	xslog.LoggerFactory
	wrappedRouter       Router
	errHandlerStrategy  func(errHandlerFunc) http.Handler
	handler             http.Handler
	ignoreTrailingSlash bool
}

func NewRouter(options ...RouterOption) (*router, error) {

	cfg := routerConfig{}

	for _, f := range options {
		f(&cfg)
	}

	if err := cfg.validate(); err != nil {
		return nil, errors.Join(ErrBadRouterConfig, err)
	}

	rt := &router{
		cfg.logf,
		cfg.router,
		nil,
		nil,
		cfg.ignoreTrailingSlash,
	}

	if cfg.errHandlerStrategyIsSet {
		rt.errHandlerStrategy = cfg.errHandlerStrategy
	}

	// prepare handler ref from base router context
	handler := http.Handler(rt.wrappedRouter)

	// inject panic handler
	if !cfg.routerIsSet {
		// Used newDefaultRouter() to set the wrappedRouter implementation
		// which is a non-nil *httprouter.Router instance. So going to set
		// the PanicHandler on that instance directly from our upper layer
		// default.
		if !cfg.panicHandlerIsSet {
			rt.wrappedRouter.(*httprouter.Router).PanicHandler = rt.defaultPanicHandler
		}
	} else {
		next := handler

		ph := cfg.panicHandler
		if !cfg.panicHandlerIsSet {
			ph = rt.defaultPanicHandler
		}

		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					ph(w, r, rec)
				}
			}()

			next.ServeHTTP(w, r)
		})
	}

	// save potentially wrapped router handler to wrapping context for serverHTTP calls
	rt.handler = handler

	// the router wrapper is now built
	return rt, nil
}

func (rt *router) Handler(method string, path string, handler http.Handler) {
	rt.wrappedRouter.Handler(method, path, handler)

	if !rt.ignoreTrailingSlash {
		return
	}

	if strings.HasSuffix(path, "/") {
		// find a path without a slash suffix if possible
		for i := len(path) - 1; i >= 0; i-- {
			if path[i] != '/' {
				rt.wrappedRouter.Handler(method, path[:i+1], handler)
				return
			}
		}
	} else {
		// just add slash to path and register
		rt.wrappedRouter.Handler(method, path+"/", handler)
	}
}

func (rt *router) HandlerFunc(method string, path string, handler http.HandlerFunc) {
	rt.Handler(method, path, handler)
}

func (rt *router) HandlerE(method string, path string, handler errHandlerFunc) {
	if rt.errHandlerStrategy != nil {
		rt.Handler(method, path, rt.errHandlerStrategy(handler))
		return
	}

	rt.Handler(method, path, rt.defaultErrHandlerStrategy(handler))
}

func (rt *router) Connect(path string, handler http.Handler) {
	rt.Handler(http.MethodConnect, path, handler)
}

func (rt *router) ConnectF(path string, handler http.HandlerFunc) {
	rt.Connect(path, handler)
}

func (rt *router) ConnectE(path string, handler errHandlerFunc) {
	rt.HandlerE(http.MethodConnect, path, handler)
}

func (rt *router) Delete(path string, handler http.Handler) {
	rt.Handler(http.MethodDelete, path, handler)
}

func (rt *router) DeleteF(path string, handler http.HandlerFunc) {
	rt.Delete(path, handler)
}

func (rt *router) DeleteE(path string, handler errHandlerFunc) {
	rt.HandlerE(http.MethodDelete, path, handler)
}

func (rt *router) Get(path string, handler http.Handler) {
	rt.Handler(http.MethodGet, path, handler)
}

func (rt *router) GetF(path string, handler http.HandlerFunc) {
	rt.Get(path, handler)
}

func (rt *router) GetE(path string, handler errHandlerFunc) {
	rt.HandlerE(http.MethodGet, path, handler)
}

func (rt *router) Head(path string, handler http.Handler) {
	rt.Handler(http.MethodHead, path, handler)
}

func (rt *router) HeadF(path string, handler http.HandlerFunc) {
	rt.Head(path, handler)
}

func (rt *router) HeadE(path string, handler errHandlerFunc) {
	rt.HandlerE(http.MethodHead, path, handler)
}

func (rt *router) Options(path string, handler http.Handler) {
	rt.Handler(http.MethodOptions, path, handler)
}

func (rt *router) OptionsF(path string, handler http.HandlerFunc) {
	rt.Options(path, handler)
}

func (rt *router) OptionsE(path string, handler errHandlerFunc) {
	rt.HandlerE(http.MethodOptions, path, handler)
}

func (rt *router) Patch(path string, handler http.Handler) {
	rt.Handler(http.MethodPatch, path, handler)
}

func (rt *router) PatchF(path string, handler http.HandlerFunc) {
	rt.Patch(path, handler)
}

func (rt *router) PatchE(path string, handler errHandlerFunc) {
	rt.HandlerE(http.MethodPatch, path, handler)
}

func (rt *router) Post(path string, handler http.Handler) {
	rt.Handler(http.MethodPost, path, handler)
}

func (rt *router) PostF(path string, handler http.HandlerFunc) {
	rt.Post(path, handler)
}

func (rt *router) PostE(path string, handler errHandlerFunc) {
	rt.HandlerE(http.MethodPost, path, handler)
}

func (rt *router) Put(path string, handler http.Handler) {
	rt.Handler(http.MethodPut, path, handler)
}

func (rt *router) PutF(path string, handler http.HandlerFunc) {
	rt.Put(path, handler)
}

func (rt *router) PutE(path string, handler errHandlerFunc) {
	rt.HandlerE(http.MethodPut, path, handler)
}

func (rt *router) Trace(path string, handler http.Handler) {
	rt.Handler(http.MethodTrace, path, handler)
}

func (rt *router) TraceF(path string, handler http.HandlerFunc) {
	rt.Trace(path, handler)
}

func (rt *router) TraceE(path string, handler errHandlerFunc) {
	rt.HandlerE(http.MethodTrace, path, handler)
}

func (rt *router) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rt.handler.ServeHTTP(w, r)
}

// // TODO: turn into a RouterOption
// func (rt *router) SetHandlerEStrategy(f func(errHandlerFunc) http.Handler) {
// 	rt.errHandlerStrategy = f
// }

// // TODO: turn into a RouterOption
// func (rt *router) SetIgnoreTrailingSlash(b bool) {
// 	rt.ignoreTrailingSlash = b
// }

func (rt *router) defaultPanicHandler(w http.ResponseWriter, r *http.Request, rec any) {

	ctx := r.Context()

	// create error context for the recover result (rec) passed to this handler
	var err error
	switch v := rec.(type) {
	case error:
		if v != nil {
			err = xerrors.WithStack(v)
			break
		}
		err = xerrors.New("RouterPanicHandler: panic value was (error)(nil)")
	case string:
		if v == "" {
			v = "RouterPanicHandler: panic value was a empty string"
		}
		err = xerrors.New(v)
	case []byte:
		msg := string(v)
		if msg == "" {
			if v == nil {
				msg = "RouterPanicHandler: panic value was a empty ([]byte)(nil)"
			} else {
				msg = "RouterPanicHandler: panic value was a empty []byte"
			}
		}
		err = xerrors.New(msg)
	case []rune:
		msg := string(v)
		if msg == "" {
			if v == nil {
				msg = "RouterPanicHandler: panic value was a empty ([]rune)(nil)"
			} else {
				msg = "RouterPanicHandler: panic value was a empty []rune"
			}
		}
		err = xerrors.New(msg)
	default:
		err = xerrors.WithStack(ErrHandlerPanicUnknownCause)
		rt.Logger(ctx).WithErr(ctx, err).Error(ctx,
			"panicking abnormally",
			slog.String("error.type", fmt.Sprintf("%T", rec)),
		)
	}

	// done writer test
	{
		wo := writeObserverFromContext(ctx)
		if wo == nil {
			rt.Logger(ctx).Error(ctx,
				"config error: WriteObserver not in context",
			)
			NewInternalErrResp(err).WriteResp(ctx, w)
			return
		}

		wStat := wo.HTTPWriteStatus()
		if wStat.Done() {
			if _, ok := err.(http.Handler); !ok {
				rt.Logger(ctx).WithErr(ctx, err).Debug(ctx,
					"an error occurred while rendering a response and a panic bubbled up somehow",
					slog.Int("rendered_status_code", wStat.StatusCode()),
					slog.Bool("connection_hijacked", wStat.Hijacked()),
				)
			} else {
				rt.Logger(ctx).WithErr(ctx, err).Error(ctx,
					"an error occurred while rendering a response and another panic error response was going to be rendered",
					slog.Int("rendered_status_code", wStat.StatusCode()),
					slog.Bool("connection_hijacked", wStat.Hijacked()),
					slog.String("remediation_hint", "fix implementation: should not attempt to rewrite responses after response has already begun sending to client"),
				)
			}
			return
		}
	}

	//
	// render a general 500 response
	//

	NewInternalErrResp(err).WriteResp(ctx, w)
}

func (rt *router) defaultErrHandlerStrategy(f errHandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

		ctx := r.Context()

		err := f(w, r)
		if err == nil {
			// Note that a nil error result here does not guarantee that the
			// client has received any part of the response much less that
			// the client was still connected by the last write call that
			// occurred in a handler.
			//
			// The standard http.ResponseWriter in golang has some buffering
			// enabled and it is not guaranteed to implement a Flusher or an
			// ErrFlusher ( though all wrappers of ResponseWriters probably
			// should compose a http.ResponseController in their struct )
			//
			// Instead the write-observer middleware handles in a best-attempt
			// fashion the detection of a client that has lost its
			// connection on response write, conveys this cause for the context
			// canceling, logs that the issue occurred (at debug level), emits
			// relevant metrics around waste relative to the client
			// id/type/origin/access-principal/request-intent,
			// and this allows layers performing a write to abstain from
			// checking the context status before attempting to render.
			//
			// An unwrapped ErrClientDisconnected error is not expected to be thrown up to
			// this level under normal circumstances since there is not much to be done
			// under those circumstances.
			return
		}
		if v := xerrors.StacktraceReleaser(err); v != nil {
			defer v.ReleaseStacktrace()
		}

		// done writer test
		{
			wo := writeObserverFromContext(ctx)
			if wo == nil {
				rt.Logger(ctx).SpanFail(ctx, nil,
					"config error: WriteObserver not in context",
					slog.String("remediation_hint", "fix implementation: write observer middleware must be in the middleware chain to handle error responses reliably"),
				)
				NewInternalErrResp(err).WriteResp(ctx, w)
				return
			}

			stat := wo.HTTPWriteStatus()

			if stat.Done() {
				if errors.Is(err, ErrClientDisconnected) {
					// the client disconnected and the layer that detects this condition
					// already either signaled up what has happened and told the parent response writer
					// what to show in the trace layers or the client disconnected in the middle of a
					// write.
					//
					// Not much else we can do as things are definitely done.
					return
				}

				if _, ok := err.(http.Handler); !ok {
					rt.Logger(ctx).SpanFail(ctx, err,
						"an error occurred while rendering a response and it bubbled up somehow",
						slog.Int("rendered_status_code", stat.StatusCode()),
						slog.Bool("connection_hijacked", stat.Hijacked()),
						slog.String("remediation_hint", "fix implementation: don't return an error from a handler if a response was already in progress or finished - return them before writing starts or handle them and return nil"),
					)
				} else {
					rt.Logger(ctx).SpanFail(ctx, err,
						"an error occurred while rendering a response and another error response was going to be rendered",
						slog.Int("rendered_status_code", stat.StatusCode()),
						slog.Bool("connection_hijacked", stat.Hijacked()),
						slog.String("remediation_hint", "fix implementation: should not attempt to rewrite responses after response has already begun sending to client"),
					)
				}
				return
			}
		}

		// so an error has occurred and no response has been rendered yet
		// unwrap the error looking for one that implements a http.Handler
		// if found, render and return
		{
			// Explicitly using errors.Unwrap to avoid
			// triggering any custom Is/As behavior that
			// could be implemented on error types
			// and could cross errors.Join boundaries.
			//
			// errors.Join is intended for aggregating multiple
			// errors into one and the unwinding behavior is
			// highly dependant on the order in which the
			// arguments to Join are specified should multiple
			// errors in the slice implement a search target
			// interface.
			//
			// This behavior is far too fragile and opaque to
			// rely in in early iterations of this error handling
			// strategy.

			// using a local scope err variable to avoid mutating
			// the outer err variable should this block not find
			// a http.Handler implementation in the error chain
			err := err

			for err != nil {
				if v, ok := err.(http.Handler); ok {
					v.ServeHTTP(w, r)
					return
				}

				err = errors.Unwrap(err)
			}
		}

		//
		// no response was rendered yet, so given the contextual information
		// available log and render a response
		//

		// TODO: note the write observer will intercept and possibly convert a
		// response here as well.
		//
		// In the context of a static handler implementation a non-disconnected
		// errored context should not result in a 504 - but rather a 503 like:
		//
		// NewErrResp(
		// 	http.StatusServiceUnavailable,
		// 	"service-unavailable",
		// 	"static handler detected context deadline exceeded likely due to some internal response timeout policy",
		// )

		if errors.Is(err, context.Canceled) {
			// could be due to an internal client timing out or the client disconnecting

			var isConnected bool
			cdo := clientDisconnectObserverFromContext(ctx)
			if cdo != nil {
				isConnected = cdo.Connected()
			} else {
				rt.Logger(ctx).SpanErr(ctx, nil,
					"config error: ClientDisconnectObserver not in context",
				)

				// assuming client is connected
				isConnected = true
			}

			if isConnected {
				// if the client is still there, we want to send a 5xx response type
				// to signal them to try again as the cause was likely due to
				// an internal client or upstream service timing out

				errRespGatewayTimeout.CausedBy(ctx, err).With(
					WithErrRespOpts().FailSpan(true),
				).WriteResp(ctx, w)
				return
			}

			// The layers bellow this one must have observed that the client already
			// disconnected and bubbled it up as a context.Canceled (incorrectly) or
			// it was caused by an internal client timing out and canceling a separate
			// context or simply poor error management.
			//
			// In any case nothing in the stack bellow this one attempted to write
			// to the ResponseWriter nor attempted to Hijack the connection.
			//
			// The client is definitely no longer connected, and it could have consciously
			// disconnected because we took too long to respond or had a connectivity
			// error due to the internet-of-things and may retry.

			var attemptedStatusCode int
			{
				type statusCodeProvider interface {
					StatusCode() int
				}
				if v, ok := err.(statusCodeProvider); ok {
					attemptedStatusCode = v.StatusCode()
				}

				// TODO: document the above behavior, or remove it in favor of calling ServeHTTP to a nop writer that records the status code
			}

			resp := errRespClientDisconnected.CausedBy(ctx, err)
			resp.loggerDisabled = true

			const errMsg = "client missed response"

			rt.Logger(ctx).WithErr(ctx, err).Debug(ctx,
				errMsg,
				slog.Int("attempted_status_code", attemptedStatusCode),
				slog.Int("rendered_status_code", errRespClientDisconnected.getStatusCode()),
			)

			xspan.RecordError(ctx, err, errMsg)

			// TODO: emit premature disconnect custom metric

			resp.ServeHTTP(w, r)
			return
		}

		if errors.Is(err, context.DeadlineExceeded) {
			// likely due to an internal client or upstream service timing out

			// TODO: test what happens if http after-read compute and reply timeout is set and reached

			errRespGatewayTimeout.CausedBy(ctx, err).With(
				WithErrRespOpts().FailSpan(true),
			).WriteResp(ctx, w)
			return
		}

		//
		// render a general 500 response
		//

		NewInternalErrResp(err).With(
			WithErrRespOpts().FailSpan(true),
		).WriteResp(ctx, w)
	})
}

var routerNotFoundHandler = NewErrResp(http.StatusNotFound, "not-found", "Route Not Found").StaticHandler()

var routerMethodNotAllowedHandler = NewErrResp(http.StatusMethodNotAllowed, "method-not-allowed", "Method Not Allowed").StaticHandler()
