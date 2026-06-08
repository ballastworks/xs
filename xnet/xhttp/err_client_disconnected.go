package xhttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ballastworks/xs/xcontext"
	"github.com/ballastworks/xs/xerrors"
	"github.com/ballastworks/xs/xsync/xrwm"
)

const StatusClientDisconnected = 499

var (
	errMsgClientDisconnectedPrefix = "client disconnected: "

	errMsgClientDisconnected = errMsgClientDisconnectedPrefix + context.Canceled.Error()

	errRespClientDisconnected = NewErrResp(
		ErrRespOpts().StatusCode(StatusClientDisconnected),
		ErrRespOpts().ErrCode("client-disconnected"),
		ErrRespOpts().DevMsg("Client Disconnected"),
	)

	ErrClientDisconnected = fmt.Errorf(errMsgClientDisconnectedPrefix+"%w", context.Canceled)
)

type errClientDisconnected struct {
	err error
}

func (e errClientDisconnected) Unwrap() error {
	return e.err
}

func (e errClientDisconnected) Is(target error) bool {
	return errors.Is(ErrClientDisconnected, target)
}

func (e errClientDisconnected) Error() string {
	if e.err == context.Canceled {
		return errMsgClientDisconnected
	}

	return errMsgClientDisconnectedPrefix + e.err.Error()
}

//
// client disconnect observer
//

type ctxKeyClientDisconnectObserver struct{}

type disconnectTrackingContext struct {
	rwm      sync.RWMutex
	ctx      context.Context
	err      error
	released atomic.Bool
}

func (dtc *disconnectTrackingContext) Connected() bool {
	if dtc.released.Load() {
		return false
	}

	return !xcontext.Done(dtc.ctx)
}

func (dtc *disconnectTrackingContext) Value(key any) any {
	if _, ok := key.(ctxKeyClientDisconnectObserver); ok {
		if dtc.released.Load() {
			return nil
		}
		return dtc
	}

	return dtc.ctx.Value(key)
}

func (dtc *disconnectTrackingContext) Deadline() (time.Time, bool) {
	return dtc.ctx.Deadline()
}

func (dtc *disconnectTrackingContext) Done() <-chan struct{} {
	return dtc.ctx.Done()
}

func (dtc *disconnectTrackingContext) Err() error {
	dtc.rwm.RLock()
	st := xrwm.RLocked
	defer func() {
		switch st {
		case xrwm.RLocked:
			dtc.rwm.RUnlock()
		case xrwm.WLocked:
			dtc.rwm.Unlock()
		}
	}()

	if err := dtc.err; err != nil {
		return err
	}

	err := xcontext.Cause(dtc.ctx)
	if err == nil {
		return nil
	}

	dtc.rwm.RUnlock()
	st = xrwm.Unlocked
	dtc.rwm.Lock()
	st = xrwm.WLocked

	if err := dtc.err; err != nil {
		return err
	}

	if errors.Is(err, context.Canceled) && !errors.Is(err, ErrClientDisconnected) {
		err = xerrors.WithStack(errClientDisconnected{err})
	} else {
		err = xerrors.WithStack(err)
	}

	dtc.err = err
	return err
}

func (dtc *disconnectTrackingContext) release() {
	if dtc == nil {
		return
	}

	func() {
		dtc.rwm.RLock()
		st := xrwm.RLocked
		defer func() {
			switch st {
			case xrwm.RLocked:
				dtc.rwm.RUnlock()
			case xrwm.WLocked:
				dtc.rwm.Unlock()
			}
		}()

		err := dtc.err
		if err == nil {
			return
		}

		dtc.rwm.RUnlock()
		st = xrwm.Unlocked
		dtc.rwm.Lock()
		st = xrwm.WLocked

		err = dtc.err
		if err == nil {
			return
		}

		dtc.err = nil

		if v := xerrors.StacktraceReleaser(err); v != nil {
			v.ReleaseStacktrace()
		}
	}()

	dtc.released.Store(true)
}

func MiddlewareClientDisconnectObserver() func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

			dtc := &disconnectTrackingContext{}
			defer dtc.release()

			ctx := r.Context()
			dtc.ctx = ctx

			// NOTE: cannot add context behaviors to dtc type because it's too
			// easy to lead to cryptic crashes due to the inner context being
			// nil should a capture have occurred causing a child context to
			// live beyond the lifecycle of a request.
			//
			// This might be acceptable given
			next.ServeHTTP(w, r.WithContext(dtc))
		})
	}
}

type clientDisconnectObserver interface {
	Connected() bool
	Err() error
}

// uDisconnectObserver is the user-land equivalent of disconnectTrackingContext
type uDisconnectObserver struct {
	rf *reqInFlight
	w  *disconnectTrackingContext
}

func (u *uDisconnectObserver) Connected() bool {
	u.rf.mustBeActive()

	return u.w.Connected()
}

func (u *uDisconnectObserver) Err() error {
	u.rf.mustBeActive()

	return u.w.Err()
}

func clientDisconnectObserverFromContext(ctx context.Context) clientDisconnectObserver {
	if v, ok := ctx.Value(ctxKeyClientDisconnectObserver{}).(*disconnectTrackingContext); ok {
		return v
	}

	return nil
}

func ClientDisconnectObserverFromContext(ctx context.Context) clientDisconnectObserver {
	if v, ok := ctx.Value(ctxKeyClientDisconnectObserver{}).(*disconnectTrackingContext); ok {
		rf := reqInFlightTracker(ctx)
		rf.mustBeActive()
		return &uDisconnectObserver{rf, v}
	}

	return nil
}
