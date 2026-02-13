package xhttp

import (
	"context"
	"net/http"
	"sync/atomic"
)

const panicReqNotInFlightMsg = "behavior sourced from context depending on an active request after request has ended"

type ctxKeyRequestInFlight struct{}

// TODO: reqInFlight could be pooled but use after release becomes a real
// tangible concern that can only be mitigated via additional state information
// maintained externally and compared to content within this data element.
type reqInFlight struct {
	inactive atomic.Bool
}

func (rf *reqInFlight) close() {
	rf.inactive.Store(true)
}

func (rf *reqInFlight) isActive() bool {
	return !rf.inactive.Load()
}

// mustBeActive panics if the receiver is nil or the receiver is not active.
//
// Should be called from a context just before and exported call of a
// middleware sourced appliance.
func (rf *reqInFlight) mustBeActive() {
	if rf != nil && rf.isActive() {
		return
	}

	panic(panicReqNotInFlightMsg)
}

func middlewareRequestInFlight(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rf := &reqInFlight{}

		ctx := context.WithValue(r.Context(), ctxKeyRequestInFlight{}, rf)
		// TODO: can add context behaviors to rf type
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func MiddlewareRequestInFlightBegin() Middleware {
	return middlewareRequestInFlight
}

func RequestInFlight(ctx context.Context) bool {

	if v := ctx.Value(ctxKeyRequestInFlight{}); v != nil {
		return v.(*reqInFlight).isActive()
	}

	return false
}

func reqInFlightTracker(ctx context.Context) *reqInFlight {
	if v := ctx.Value(ctxKeyRequestInFlight{}); v != nil {
		return v.(*reqInFlight)
	}

	return nil
}

// reqInFlightContextDone signals that the connection associated with
// the provided context has been hijacked and the request is no longer in
// flight technically or that the user provided handler has completed its
// execution.
//
// This means that any capabilities sourced from the context that depend
// on the request being active (such as logging, tracing, etc.) should
// cease their operations and most likely panic if they are used after
// this occurs.
func reqInFlightContextDone(ctx context.Context) {
	if v := ctx.Value(ctxKeyRequestInFlight{}); v != nil {
		v.(*reqInFlight).close()
	}
}

func MiddlewareRequestInFlightEnd() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			{
				ctx := r.Context()
				defer reqInFlightContextDone(ctx)
			}

			next.ServeHTTP(w, r)
		})
	}
}
