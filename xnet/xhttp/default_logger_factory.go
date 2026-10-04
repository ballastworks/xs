package xhttp

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"

	"github.com/ballastworks/xs/internal/ctx_slog"
	"github.com/ballastworks/xs/xlog/xslog"
)

// TODO: move discoverable exported logger details to xplog

type defaultLoggerFactoryFoundationProxy struct{}

func (defaultLoggerFactoryFoundationProxy) Enabled(ctx context.Context, level slog.Level) bool {
	return xslog.DefaultFactory().Enabled(ctx, level)
}

func (defaultLoggerFactoryFoundationProxy) Logger(ctx context.Context) xslog.Logger {
	return xslog.DefaultFactory().Logger(ctx)
}

type defaultWriteTrackingLoggerFactoryFoundationProxy struct {
	defaultLoggerFactoryFoundationProxy
}

// newWriteTrackingLogger returns logger with write tracking added, which the
// request correlation log relies on.
func newWriteTrackingLogger(ctx context.Context, logger xslog.Logger) (xslog.Logger, error) {
	op := xslog.LoggerOpts()

	// TODO: remove the need to convert to a slog.Handler
	return xslog.New(
		op.Handler(logger.SlogHandler(ctx)),
		op.TrackWrites(true),
	)
}

func checkDefaultWriteTrackingLoggerFactoryFoundationProxyError(err error) {
	if err == nil {
		return
	}

	panic(fmt.Errorf("xhttp request logger: cannot add write tracking to the logger from the default xslog logger factory: %w", err))
}

func (defaultWriteTrackingLoggerFactoryFoundationProxy) Logger(ctx context.Context) xslog.Logger {
	logger, err := newWriteTrackingLogger(ctx, xslog.DefaultFactory().Logger(ctx))
	checkDefaultWriteTrackingLoggerFactoryFoundationProxyError(err)

	return logger
}

func checkTrackingLoggerFactoryWrapperCallLoggerError(err error) {
	if err == nil {
		return
	}

	panic(fmt.Errorf("xhttp request logger: cannot add write tracking to the logger from the LoggerFactory option: %w", err))
}

type trackingLoggerFactoryWrapper struct {
	xslog.LoggerFactory
}

func (w *trackingLoggerFactoryWrapper) Logger(ctx context.Context) xslog.Logger {
	logger, err := newWriteTrackingLogger(ctx, w.LoggerFactory.Logger(ctx))
	checkTrackingLoggerFactoryWrapperCallLoggerError(err)

	return logger
}

func newWriteTrackingLoggerFactoryWrapper(logf xslog.LoggerFactory) xslog.LoggerFactory {
	return &trackingLoggerFactoryWrapper{logf}
}

var defaultLoggerFactoryFoundation = &defaultLoggerFactoryFoundationProxy{}
var defaultWriteTrackingLoggerFactoryFoundation = &defaultWriteTrackingLoggerFactoryFoundationProxy{}

type boxedLoggerFactory struct {
	v xslog.LoggerFactory
}

func checkNewDefaultLoggerFactoryAtomic(err error) {
	if err != nil {
		panic(err)
	}
}

func newDefaultLoggerFactoryAtomic() atomic.Value {
	logf, err := NewRequestLoggerFactory()
	checkNewDefaultLoggerFactoryAtomic(err)

	var v atomic.Value
	v.Store(boxedLoggerFactory{logf})

	return v
}

var defaultLoggerFactoryAtomic = newDefaultLoggerFactoryAtomic()

func DefaultLoggerFactory() xslog.LoggerFactory {
	return defaultLoggerFactoryAtomic.Load().(boxedLoggerFactory).v
}

// SetDefaultFactory replaces the old default response factory and returns the old instance
func SetDefaultLoggerFactory(factory xslog.LoggerFactory) xslog.LoggerFactory {
	if factory == nil {
		panic("nil logger factory")
	}

	old := defaultLoggerFactoryAtomic.Swap(boxedLoggerFactory{factory})
	return old.(boxedLoggerFactory).v
}

type defaultLoggerFactoryProxy struct{}

func (defaultLoggerFactoryProxy) Enabled(ctx context.Context, level slog.Level) bool {
	return DefaultLoggerFactory().Enabled(ctx, level)
}

func (defaultLoggerFactoryProxy) Logger(ctx context.Context) xslog.Logger {
	return DefaultLoggerFactory().Logger(ctx)
}

var defaultLoggerFactory = &defaultLoggerFactoryProxy{}

func MiddlewareAddLoggerFactoryToContext(factory xslog.LoggerFactory) Middleware {
	if factory == nil {
		panic("nil logger factory")
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := ctx_slog.ContextWithLoggerFactory(r.Context(), factory)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
