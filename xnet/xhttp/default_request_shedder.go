package xhttp

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"

	"github.com/ballastworks/xs/xcontext/xspan"
	"github.com/ballastworks/xs/xsync/xrwm"
)

type defaultReqShedder struct {
	rwm      sync.RWMutex
	shedding bool
	wg       sync.WaitGroup
}

func (drs *defaultReqShedder) ShedRequestMiddleware() func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		const shedStatus = http.StatusServiceUnavailable
		shedBody := []byte(`{"errcode":"server-shutting-down"}` + "\n")
		shedContentLength := strconv.Itoa(len(shedBody))

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if handled, shedReq := drs.shedRequest(next, w, r); shedReq {
				if handled {
					return
				}

				{
					h := w.Header()
					h.Add("Content-Type", "application/json")
					h.Add("Content-Length", shedContentLength)
				}

				w.WriteHeader(shedStatus)

				if _, err := w.Write(shedBody); err != nil {
					// ignore the error, but at least record the error and mark the span as failed
					xspan.Fail(r.Context(), err, "")
				}

				return
			}

			// not shedding the request
			// so keep track of its completion

			defer drs.wg.Done()

			next.ServeHTTP(w, r)

			type errFlusher interface {
				FlushError() error
			}

			// ensure the transaction content has all made its way to the client completely
			if v, ok := w.(http.Flusher); ok {
				v.Flush()
			} else if v, ok := w.(errFlusher); ok {
				if err := v.FlushError(); err != nil && !errors.Is(err, http.ErrNotSupported) {
					// TODO: allow for context aware logging in the future
					//
					// not much we can do if the client fails to receive all content
					// in a timely fashion
					ctx := r.Context()
					logger := defaultLoggerFactory.Logger(ctx)
					logger.WithErr(ctx, err).Debug(ctx,
						"client failed to read full response",
					)
				}
			}
		})
	}
}

// ShedRequests signals to the middleware to start shedding requests
// and may wait until all in progress requests finish before returning
func (drs *defaultReqShedder) ShedRequests(context.Context) {
	defer drs.wg.Wait()

	drs.rwm.Lock()
	defer drs.rwm.Unlock()

	drs.shedding = true
}

func (drs *defaultReqShedder) shedRequest(next http.Handler, w http.ResponseWriter, r *http.Request) (_handled bool, _shedReq bool) {
	var handled bool

	drs.rwm.RLock()
	st := xrwm.RLocked

	defer func() {
		if st == xrwm.RLocked {
			drs.rwm.RUnlock()
		}
	}()

	// note, it is invalid to attempt to handle the request when shedding is not enabled

	shedding := drs.shedding
	if !shedding {
		// wg.Add() must always occur under the protection of the lock
		// and only if not shedding
		drs.wg.Add(1)

		return handled, shedding
	}

	// rest of this function after the unlock call can run concurrently with other runtimes
	// attempting to alter the shedding state
	//
	// note that should never happen anyways since we set that once and only once on shutdown signal processing
	//
	// so the only concurrency is with other requests getting shed where we may attempt to handle it
	// differently or call the next() func and let it proceed
	drs.rwm.RUnlock()
	st = xrwm.Unlocked

	// NOTE: make a copy of this implementation if you need to customize the path
	// or raise a PR to make this mutable in a safe way
	if r.Method == http.MethodGet {
		switch r.URL.Path {
		case "/healthcheck", "/healthcheck/":
			handled = true
			next.ServeHTTP(w, r)
			return handled, shedding
		}
	}

	return handled, shedding
}
