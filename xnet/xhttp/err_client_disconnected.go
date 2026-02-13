package xhttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"github.com/ballastworks/xs/xcontext"
	"github.com/ballastworks/xs/xerrors"
	"github.com/ballastworks/xs/xsync/xrwm"
)

const StatusClientDisconnected = 499

var (
	errMsgClientDisconnectedPrefix = "client disconnected: "

	errMsgClientDisconnected = errMsgClientDisconnectedPrefix + context.Canceled.Error()

	errRespClientDisconnected = NewErrResp(StatusClientDisconnected, "client-disconnected", "Client Disconnected")

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

var disconnectTrackingContextPool = sync.Pool{
	New: func() any {
		return &disconnectTrackingContext{}
	},
}

type disconnectTrackingContextState struct {
	context.Context
	err error
}

type disconnectTrackingContext struct {
	rwm sync.RWMutex
	disconnectTrackingContextState
}

func (dtc *disconnectTrackingContext) Connected() bool {
	return !xcontext.Done(dtc.Context)
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

	err := xcontext.Cause(dtc.Context)
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

	dtc.disconnectTrackingContextState = disconnectTrackingContextState{}
	disconnectTrackingContextPool.Put(dtc)
}

func MiddlewareClientDisconnectObserver() func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

			dtc := disconnectTrackingContextPool.Get().(*disconnectTrackingContext)
			defer dtc.release()

			ctx := r.Context()
			ctx = context.WithValue(ctx, ctxKeyClientDisconnectObserver{}, dtc)

			dtc.Context = ctx
			// TODO: can add context behaviors to dtc type
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
