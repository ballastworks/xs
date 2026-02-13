package xhttp

import (
	"bufio"
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync"

	"github.com/ballastworks/xs/xcontext"
	"github.com/ballastworks/xs/xcontext/xspan"
	"github.com/ballastworks/xs/xlog/xslog"
	"github.com/ballastworks/xs/xsync/xrwm"
)

// TODO: consider exposing more status info, such as whether the client disconnected
// during or before a write operation.

// writeObserverState has the internal state of a writeObserver and all public methods
// are exposed through the writeObserver struct to the outside world.
//
// If modifying this struct, ensure that new additions are NOT exposed publicly unless
// explicitly intended.
type writeObserverState struct {
	http.ResponseWriter
	*http.ResponseController

	ctx           context.Context
	logf          xslog.LoggerFactory
	disconnectErr error
	statusCode    int
	done          bool
	hijacked      bool
}

type writeObserver struct {
	m sync.RWMutex
	writeObserverState
}

type writeObserverStatusResp interface {
	Done() bool
	Hijacked() bool
	StatusCode() int
}

type writeObserverStatus struct {
	statusCode int
	done       bool
	hijacked   bool
}

func (wos writeObserverStatus) Done() bool {
	return wos.done
}

func (wos writeObserverStatus) Hijacked() bool {
	return wos.hijacked
}

func (wos writeObserverStatus) StatusCode() int {
	return wos.statusCode
}

func (wo *writeObserver) HTTPWriteStatus() writeObserverStatusResp {
	wo.m.RLock()
	defer wo.m.RUnlock()

	return writeObserverStatus{
		wo.statusCode,
		wo.done,
		wo.hijacked,
	}
}

func (wo *writeObserver) cdo() clientDisconnectObserver {
	return clientDisconnectObserverFromContext(wo.ctx)
}

func (wo *writeObserver) handleNilClientDisconnectObserver() {
	ctx := wo.ctx

	logger := wo.logf.Logger(ctx)
	logger.Error(ctx,
		"config error: WriteObserver not in context",
	)
}

func (wo *writeObserver) clientDisconnectErr(cdo clientDisconnectObserver) error {
	if cdo == nil {
		wo.handleNilClientDisconnectObserver()
		return nil
	}

	return cdo.Err()
}

func (wo *writeObserver) clientDisconnected(cdo clientDisconnectObserver) bool {
	if cdo == nil {
		wo.handleNilClientDisconnectObserver()
		return false
	}

	return !cdo.Connected()
}

func (wo *writeObserver) reset() {
	wo.m.Lock()
	defer wo.m.Unlock()

	wo.writeObserverState = writeObserverState{}
}

func (wo *writeObserver) setDoneOnHijack() error {
	wo.m.RLock()
	st := xrwm.RLocked
	defer func() {
		switch st {
		case xrwm.RLocked:
			wo.m.RUnlock()
		case xrwm.WLocked:
			wo.m.Unlock()
		}
	}()

	if err := wo.disconnectErr; err != nil {
		return err
	}

	if !wo.done || !wo.hijacked {
		wo.m.RUnlock()
		st = xrwm.Unlocked
		wo.m.Lock()
		st = xrwm.WLocked

		if err := wo.disconnectErr; err != nil {
			return err
		}

		if err := wo.clientDisconnectErr(wo.cdo()); err != nil {
			wo.setDisconnectErr(err)
			return wo.disconnectErr
		}

		wo.done = true
		wo.hijacked = true
	}

	return nil
}

func (wo *writeObserver) setDoneOnWrite() error {
	wo.m.RLock()
	st := xrwm.RLocked
	defer func() {
		switch st {
		case xrwm.RLocked:
			wo.m.RUnlock()
		case xrwm.WLocked:
			wo.m.Unlock()
		}
	}()

	if err := wo.disconnectErr; err != nil {
		return err
	}

	if !wo.done || wo.statusCode == 0 {
		wo.m.RUnlock()
		st = xrwm.Unlocked
		wo.m.Lock()
		st = xrwm.WLocked

		if err := wo.disconnectErr; err != nil {
			return err
		}

		if wo.statusCode == 0 {
			wo.statusCode = http.StatusOK
		}

		if err := xcontext.Cause(wo.ctx); errors.Is(err, ErrClientDisconnected) {
			wo.setDisconnectErr(err)
			return wo.disconnectErr
		} else if err := wo.clientDisconnectErr(wo.cdo()); err != nil {
			wo.setDisconnectErr(err)
			return wo.disconnectErr
		} else {
			wo.done = true
		}
	} else if err := xcontext.Cause(wo.ctx); errors.Is(err, ErrClientDisconnected) {
		wo.m.RUnlock()
		st = xrwm.Unlocked
		wo.m.Lock()
		st = xrwm.WLocked

		if wo.disconnectErr == nil {
			wo.setDisconnectErr(err)
		}

		return wo.disconnectErr
	} else if cdo := wo.cdo(); wo.clientDisconnected(cdo) {
		wo.m.RUnlock()
		st = xrwm.Unlocked
		wo.m.Lock()
		st = xrwm.WLocked

		if wo.disconnectErr == nil {
			wo.setDisconnectErr(cdo.Err())
		}

		return wo.disconnectErr
	}

	return nil
}

func (wo *writeObserver) setDoneOnWriteHeader(statusCode int) error {
	wo.m.RLock()
	st := xrwm.RLocked
	defer func() {
		switch st {
		case xrwm.RLocked:
			wo.m.RUnlock()
		case xrwm.WLocked:
			wo.m.Unlock()
		}
	}()

	if err := wo.disconnectErr; err != nil {
		return err
	}

	if !wo.done || wo.statusCode == 0 {
		wo.m.RUnlock()
		st = xrwm.Unlocked
		wo.m.Lock()
		st = xrwm.WLocked

		if err := wo.disconnectErr; err != nil {
			return err
		}

		if wo.statusCode == 0 {
			if statusCode == 0 {
				statusCode = http.StatusOK
			}
			wo.statusCode = statusCode
		}

		if err := xcontext.Cause(wo.ctx); errors.Is(err, ErrClientDisconnected) {
			wo.setDisconnectErr(err)

			return wo.disconnectErr
		} else if err := wo.clientDisconnectErr(wo.cdo()); err != nil {
			wo.setDisconnectErr(err)

			return wo.disconnectErr
		} else {
			wo.done = true
		}
	} else if err := xcontext.Cause(wo.ctx); errors.Is(err, ErrClientDisconnected) {
		wo.m.RUnlock()
		st = xrwm.Unlocked
		wo.m.Lock()
		st = xrwm.WLocked

		if wo.disconnectErr == nil {
			wo.setDisconnectErr(err)
		}

		return wo.disconnectErr
	} else if cdo := wo.cdo(); wo.clientDisconnected(cdo) {
		wo.m.RUnlock()
		st = xrwm.Unlocked
		wo.m.Lock()
		st = xrwm.WLocked

		if wo.disconnectErr == nil {
			wo.setDisconnectErr(cdo.Err())
		}

		return wo.disconnectErr
	}

	return nil
}

// setDisconnectErr must only be called if the internal disconnectErr member is nil and under
// the protection of the write lock
func (wo *writeObserver) setDisconnectErr(err error) {
	wo.disconnectErr = err

	if wo.disconnectDetectedHandler(wo.ctx, wo.ResponseWriter, err, wo.statusCode, wo.done) {
		wo.done = true
	}
}

// disconnectDetectedHandler returns true if it handled writing a response
// to the passed in ResponseWriter context (parent writer without write observer)
// and the write observer should consider the response as done.
//
// Is also expected to log details about the attempted response write and emit
// metrics relating client disconnects to response codes that may have been written
// and observed by the client and/or premature client disconnect metrics
func (wo *writeObserver) disconnectDetectedHandler(ctx context.Context, w http.ResponseWriter, err error, attemptedStatusCode int, writesAlreadyStarted bool) bool {
	// note: it is impossible for attemptedStatusCode to be 0

	logger := wo.logf.Logger(ctx)
	if writesAlreadyStarted {
		const errMsg = "client missed part of response"

		xspan.Fail(ctx, err, errMsg)

		logger.WithErr(ctx, err).Debug(ctx,
			errMsg,
			slog.Int("http.status_code", attemptedStatusCode),
		)

		return false
	}

	const errMsg = "client missed response"

	xspan.Fail(ctx, err, errMsg)

	logger.WithErr(ctx, err).Debug(ctx,
		errMsg,
		slog.Int("attempted_status_code", attemptedStatusCode),
		slog.Int("rendered_status_code", StatusClientDisconnected),
	)

	// TODO: emit premature disconnect custom metric

	resp := errRespClientDisconnected.CausedBy(ctx, err)
	resp.loggerDisabled = true
	resp.WriteResp(ctx, w)
	return true
}

func (wo *writeObserver) Write(b []byte) (int, error) {
	if err := wo.setDoneOnWrite(); err != nil {
		return 0, err
	}

	return wo.ResponseWriter.Write(b)
}

func (wo *writeObserver) WriteHeader(statusCode int) {
	if err := wo.setDoneOnWriteHeader(statusCode); err != nil {
		return
	}

	wo.ResponseWriter.WriteHeader(statusCode)
}

func (wo *writeObserver) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	reqInFlightContextDone(wo.ctx)

	if err := wo.setDoneOnHijack(); err != nil {
		return nil, nil, err
	}

	return wo.ResponseController.Hijack()
}

func releaseWriteObserverToPool(wo *writeObserver) {
	// only put it back in the pool if not done
	// or if done and not hijacked
	//
	// otherwise async processing could be going on through the observer
	// because the connection was hijacked
	s := wo.HTTPWriteStatus()
	if done := s.Done(); !done || !s.Hijacked() {
		wo.reset()
		writeObserverPool.Put(wo)
	}
}

type ctxKeyWriteObserver struct{}

type writeObserverConfig struct {
	logf xslog.LoggerFactory
}

type writeObserverOpts struct{}

func WriteObserverOpts() writeObserverOpts {
	return writeObserverOpts{}
}

func (writeObserverOpts) LoggerFactory(logf xslog.LoggerFactory) WriteObserverOption {
	return func(cfg *writeObserverConfig) {
		cfg.logf = logf
	}
}

type WriteObserverOption func(*writeObserverConfig)

func MiddlewareWriteObserver(options ...WriteObserverOption) func(next http.Handler) http.Handler {
	cfg := writeObserverConfig{}

	for _, f := range options {
		f(&cfg)
	}

	if cfg.logf == nil {
		cfg.logf = DefaultLoggerFactory()
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

			wo := newWriteObserver(ctx, w, cfg.logf)
			defer releaseWriteObserverToPool(wo)

			ctx = context.WithValue(ctx, ctxKeyWriteObserver{}, wo)
			next.ServeHTTP(wo, r.WithContext(ctx))
		})
	}
}

type writeStatusObserver interface {
	HTTPWriteStatus() writeObserverStatusResp
}

// uWriteObserver is the user-land equivalent of writeObserver
type uWriteObserver struct {
	rf *reqInFlight
	w  *writeObserver
}

type uWriteObserverStatus struct {
	rf *reqInFlight
	w  writeObserverStatusResp
}

func (u *uWriteObserver) HTTPWriteStatus() writeObserverStatusResp {
	u.rf.mustBeActive()

	return &uWriteObserverStatus{u.rf, u.w.HTTPWriteStatus()}
}

func (u *uWriteObserverStatus) Done() bool {
	u.rf.mustBeActive()

	return u.w.Done()
}

func (u *uWriteObserverStatus) Hijacked() bool {
	u.rf.mustBeActive()

	return u.w.Hijacked()
}

func (u *uWriteObserverStatus) StatusCode() int {
	u.rf.mustBeActive()

	return u.w.StatusCode()
}

func writeObserverFromContext(ctx context.Context) writeStatusObserver {
	if v, ok := ctx.Value(ctxKeyWriteObserver{}).(*writeObserver); ok {
		return v
	}

	return nil
}

func WriteObserverFromContext(ctx context.Context) writeStatusObserver {
	if v, ok := ctx.Value(ctxKeyWriteObserver{}).(*writeObserver); ok {
		rf := reqInFlightTracker(ctx)
		rf.mustBeActive()
		return &uWriteObserver{rf, v}
	}

	return nil
}

var writeObserverPool = sync.Pool{
	New: func() any {
		return &writeObserver{}
	},
}

func newWriteObserver(ctx context.Context, w http.ResponseWriter, logf xslog.LoggerFactory) *writeObserver {
	wo := writeObserverPool.Get().(*writeObserver)
	wo.logf = logf

	wo.ctx = ctx
	wo.ResponseWriter = w
	wo.ResponseController = http.NewResponseController(w)

	return wo
}
