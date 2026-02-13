package xhttp

import (
	"context"
	"errors"
	"net/http"
	"net/textproto"
	"sync"
	"time"

	"github.com/ballastworks/xs/xsync/xrwm"
)

// TODO: understand both and what should change here:
// - https://www.w3.org/TR/trace-context/
// - https://www.w3.org/TR/trace-context-2/
//
// headers: [traceparent, tracestate]

var (
	ErrNilKeysFunc = errors.New("nil KeysFunc value")
)

type traceAdderCtxKey struct{}

func envoyKeysFunc() []string {
	return []string{
		// light step
		"X-Ot-Span-Context",
		// zipkin
		"B3", "X-B3-Traceid", "X-B3-Spanid", "X-B3-Parentspanid", "X-B3-Sampled", "X-B3-Flags",
		// datadog
		"X-Datadog-Trace-Id", "X-Datadog-Parent-Id", "X-Datadog-Sampling-Priority",
		// sky walking
		"Sw8",
		// amazon xray
		"X-Amzn-Trace-Id",
	}
}

type requestTraceAdder interface {
	AddTraceToHttpRequest(*http.Request) *http.Request
}

type traceMediatorState struct {
	ctx         context.Context
	h           http.Header
	traceHeader http.Header
	computed    bool
	keysFunc    func() []string
}

// traceMediator is a concurrency-safe implementation of a RequestTraceAdder
type traceMediator struct {
	rwm sync.RWMutex
	traceMediatorState
}

func (tm *traceMediator) Value(key any) any {
	if _, ok := key.(traceAdderCtxKey); ok {
		return tm
	}

	return tm.ctx.Value(key)
}

func (tm *traceMediator) Deadline() (deadline time.Time, ok bool) {
	return tm.ctx.Deadline()
}

func (tm *traceMediator) Done() <-chan struct{} {
	return tm.ctx.Done()
}

func (tm *traceMediator) Err() error {
	return tm.ctx.Err()
}

var traceMediatorPool = sync.Pool{
	New: func() any {
		return &traceMediator{}
	},
}

func newTraceMediatorFromPool(ctx context.Context, h http.Header, keysFunc func() []string) *traceMediator {
	tm := traceMediatorPool.Get().(*traceMediator)
	tm.init(ctx, h, keysFunc)

	return tm
}

func newTraceMediator(ctx context.Context, h http.Header, keysFunc func() []string) *traceMediator {
	tm := &traceMediator{}
	tm.init(ctx, h, keysFunc)
	return tm
}

func poolTraceMediator(tm *traceMediator) {
	if tm != nil {
		tm.dispose()
		traceMediatorPool.Put(tm)
	}
}

func (tm *traceMediator) init(ctx context.Context, h http.Header, keysFunc func() []string) {
	tm.traceMediatorState = traceMediatorState{
		ctx:      ctx,
		h:        h,
		keysFunc: keysFunc,
	}
}

func (tm *traceMediator) HTTPMiddlewareReleasesTraceAdderToPool() bool {
	return true
}

func (tm *traceMediator) dispose() {
	tm.traceMediatorState = traceMediatorState{}
}

func (tm *traceMediator) header() http.Header {
	if tm.h == nil {
		return nil
	}

	tm.rwm.RLock()
	st := xrwm.RLocked

	defer func() {
		switch st {
		case xrwm.RLocked:
			tm.rwm.RUnlock()
		case xrwm.WLocked:
			tm.rwm.Unlock()
		}
	}()

	if tm.computed {
		return tm.traceHeader
	}

	tm.rwm.RUnlock()
	st = xrwm.Unlocked
	tm.rwm.Lock()
	st = xrwm.WLocked

	if !tm.computed {

		h := http.Header{}

		names := tm.keysFunc()

		for _, key := range names {
			values := tm.h[key]
			if len(values) > 0 {
				h[key] = []string{values[0]}
			}
		}

		if len(h) > 0 {
			tm.traceHeader = h
		}

		tm.computed = true
	}

	return tm.traceHeader
}

func (tm *traceMediator) AddTraceToHttpRequest(req *http.Request) *http.Request {
	if req == nil {
		return nil
	}

	dst := req.Header
	if dst == nil {
		dst = http.Header{}
		req.Header = dst
	}

	for k, v := range tm.header() {
		dst[k] = v
	}

	return req
}

type noPoolTraceMediator struct {
	*traceMediator
}

func (tm noPoolTraceMediator) HTTPMiddlewareReleasesTraceAdderToPool() bool {
	return false
}

type traceMiddlewareConfig struct {
	keysFunc              func() []string
	keysFuncNotNormalized bool
	usePool               bool
}

type TraceMiddlewareOption func(*traceMiddlewareConfig)
type traceMiddlewareOpts struct{}

func TraceMiddlewareOpts() traceMiddlewareOpts {
	return traceMiddlewareOpts{}
}

// KeysFunc specifies the function that returns the list of header keys
// to extract tracing information from incoming requests.
//
// The keys returned by the function will be normalized to
// canonical MIME header key format (e.g. "X-B3-Traceid" becomes "X-B3-TraceID").
//
// Note that the middleware lazily normalizes the keys which may cause the slice
// contents to be modified after the option is applied. If you want to avoid
// changing the slice contents after applying the option clone the slice before
// passing it to the option.
func (traceMiddlewareOpts) KeysFunc(f func() []string) TraceMiddlewareOption {
	return func(cfg *traceMiddlewareConfig) {
		cfg.keysFunc = f
		cfg.keysFuncNotNormalized = true
	}
}

func (traceMiddlewareOpts) UsePool(b bool) TraceMiddlewareOption {
	return func(cfg *traceMiddlewareConfig) {
		cfg.usePool = b
	}
}

func (cfg *traceMiddlewareConfig) validate() error {

	if cfg.keysFunc == nil {
		return ErrNilKeysFunc
	}

	if cfg.keysFuncNotNormalized {

		if f := cfg.keysFunc; f != nil {
			values := f()

			for i := range values {
				values[i] = textproto.CanonicalMIMEHeaderKey(values[i])
			}

			cfg.keysFunc = func() []string {
				return values
			}
		}

		cfg.keysFuncNotNormalized = false
	}

	return nil
}

func NewTraceMiddleware(options ...TraceMiddlewareOption) (func(http.Handler) http.Handler, error) {

	cfg := traceMiddlewareConfig{
		keysFunc: envoyKeysFunc,
	}

	for _, f := range options {
		f(&cfg)
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	if cfg.usePool {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				tm := newTraceMediatorFromPool(r.Context(), r.Header, cfg.keysFunc)
				defer poolTraceMediator(tm)

				next.ServeHTTP(w, r.WithContext(tm))
			})
		}, nil
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			tm := newTraceMediator(r.Context(), r.Header, cfg.keysFunc)
			next.ServeHTTP(w, r.WithContext(tm))
		})
	}, nil
}

func traceAdderFromContext(ctx context.Context) requestTraceAdder {
	if v, ok := ctx.Value(traceAdderCtxKey{}).(requestTraceAdder); ok {
		//
		// note that it is impossible right now (2025-12-29) for a RequestTraceAdder
		// in the context for the key traceAdderCtxKey{} to never implement
		// `HTTPMiddlewareReleasesTraceAdderToPool() bool` because the only way to
		// add one to the context is via NewTraceMiddleware above, which
		// always adds either a traceMediator or noPoolTraceMediator,
		// both of which implement that method.
		//
		// so going to comment out this implementation that performs safety checks
		// and depend on the invariant that all RequestTraceAdders in context
		// for the key traceAdderCtxKey{} implement that method.
		//

		// if vt, ok := v.(interface{ HTTPMiddlewareReleasesTraceAdderToPool() bool }); !ok || !vt.HTTPMiddlewareReleasesTraceAdderToPool() {
		// 	return v
		// }

		if !v.(interface{ HTTPMiddlewareReleasesTraceAdderToPool() bool }).HTTPMiddlewareReleasesTraceAdderToPool() {
			return v
		}

		if rf := reqInFlightTracker(ctx); rf != nil && rf.isActive() {
			return v
		}
	}

	return nil
}

type uRequestTraceAdder struct {
	rf *reqInFlight
	w  requestTraceAdder
}

func (u *uRequestTraceAdder) AddTraceToHttpRequest(req *http.Request) *http.Request {
	u.rf.mustBeActive()

	return u.w.AddTraceToHttpRequest(req)
}

func TraceAdderFromContext(ctx context.Context) requestTraceAdder {
	if v, ok := ctx.Value(traceAdderCtxKey{}).(requestTraceAdder); ok {
		//
		// note that it is impossible right now (2025-12-29) for a RequestTraceAdder
		// in the context for the key traceAdderCtxKey{} to never implement
		// `HTTPMiddlewareReleasesTraceAdderToPool() bool` because the only way to
		// add one to the context is via NewTraceMiddleware above, which
		// always adds either a traceMediator or noPoolTraceMediator,
		// both of which implement that method.
		//
		// so going to comment out this implementation that performs safety checks
		// and depend on the invariant that all RequestTraceAdders in context
		// for the key traceAdderCtxKey{} implement that method.
		//

		// if vt, ok := v.(interface{ HTTPMiddlewareReleasesTraceAdderToPool() bool }); !ok || !vt.HTTPMiddlewareReleasesTraceAdderToPool() {
		// 	return v
		// }

		if !v.(interface{ HTTPMiddlewareReleasesTraceAdderToPool() bool }).HTTPMiddlewareReleasesTraceAdderToPool() {
			return v
		}

		rf := reqInFlightTracker(ctx)
		rf.mustBeActive()
		return &uRequestTraceAdder{rf, v}
	}

	return nil
}

func addTrace(ctx context.Context, req *http.Request) *http.Request {

	ta := traceAdderFromContext(ctx)
	if ta == nil {
		return req
	}

	return ta.AddTraceToHttpRequest(req)
}
