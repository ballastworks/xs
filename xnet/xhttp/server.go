package xhttp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/ballastworks/xs/xlog/xslog"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

const (
	errMsgShutdownDeadlineExceeded = "graceful shutdown deadline exceeded"
	defaultHttpSpanOperation       = "http.request"
)

var (
	//
	// classifications
	//
	ErrBadSrvConfig   = errors.New("bad server config")
	ErrServeFailed    = errors.New("failed to serve")
	ErrShutdownFailed = errors.New("failed to gracefully shutdown")
	ErrNilSrvHandler  = errors.New("nil http.Server.Handler")

	//
	// specific errors
	//
	ErrListenAndServePanic      = errors.New("panic in ListenAndServe")
	ErrShutdownDeadlineExceeded = &errShutdownDeadlineExceeded{}
)

// errShutdownDeadlineExceeded is a context.DeadlineExceeded error type
// classification specifically for server shutdown deadline breaches.
type errShutdownDeadlineExceeded struct{}

func (e errShutdownDeadlineExceeded) Error() string {
	return errMsgShutdownDeadlineExceeded
}

func (e errShutdownDeadlineExceeded) Is(target error) bool {
	switch target {
	case ErrShutdownDeadlineExceeded, context.DeadlineExceeded:
		return true
	default:
		return false
	}
}

type Server struct {
	xslog.LoggerFactory
	afterShutdownFuncs          []func(error)
	shutdownTimeout             time.Duration
	requestShedder              RequestShedder
	server                      *http.Server
	shedRequestsOnShutdown      bool
	disableKeepAlivesOnShutdown bool
}

type defaultTraceMiddlewareChainConfig struct {
	spanOperation           string
	middlewareLoggerOptions []MiddlewareLoggerOption
}

type DefaultTraceMiddlewareChainOption func(*defaultTraceMiddlewareChainConfig)

func DefaultTraceMiddlewareChainOpts() DefaultTraceMiddlewareChainOptions {
	return DefaultTraceMiddlewareChainOptions{}
}

type DefaultTraceMiddlewareChainOptions struct{}

func (DefaultTraceMiddlewareChainOptions) MiddlewareLoggerOptions(v ...MiddlewareLoggerOption) DefaultTraceMiddlewareChainOption {
	return func(cfg *defaultTraceMiddlewareChainConfig) {
		cfg.middlewareLoggerOptions = v
	}
}

func (DefaultTraceMiddlewareChainOptions) SpanOperation(v string) DefaultTraceMiddlewareChainOption {
	return func(cfg *defaultTraceMiddlewareChainConfig) {
		cfg.spanOperation = v
	}
}

func DefaultTraceMiddlewareChain(options ...DefaultTraceMiddlewareChainOption) MiddlewareChain {
	cfg := defaultTraceMiddlewareChainConfig{
		// empty
	}

	for _, f := range options {
		f(&cfg)
	}

	trace, err := NewTraceMiddleware()
	if err != nil {
		panic(err)
	}

	spanOperation := cfg.spanOperation
	if spanOperation == "" {
		spanOperation = defaultHttpSpanOperation
	}

	return UnsafeSliceToMiddlewareChain(
		MiddlewareRequestInFlightBegin(),
		MiddlewareLogger(cfg.middlewareLoggerOptions...),
		trace,
		func(next http.Handler) http.Handler {
			return otelhttp.NewHandler(next, spanOperation)
		},
	)
}

func EnrichedTraceMiddlewareChain() MiddlewareChain {
	return DefaultTraceMiddlewareChain(
		DefaultTraceMiddlewareChainOpts().MiddlewareLoggerOptions(
			MiddlewareLoggerOpts().EmitRequestCorrelationLogs(true),
		),
	)
}

func DefaultBeforeHandlerMiddlewareChain(logf xslog.LoggerFactory) MiddlewareChain {
	if logf == nil {
		panic("nil logger factory")
	}

	return UnsafeSliceToMiddlewareChain(
		MiddlewareClientDisconnectObserver(),
		MiddlewareWriteObserver(WriteObserverOpts().LoggerFactory(logf)),
		MiddlewareRequestInFlightEnd(),
	)
}

// NewServer returns a new Server instance with the provided options.
//
// Aspects such as logger factory instance are resolved at initialization
// time so make sure to set the global default logger factory before calling
// this function or use the LoggerOpts().LoggerFactory(...) option if avoiding
// the overhead / risks of the default logger factory is desirable.
func NewServer(options ...SrvOption) (*Server, error) {

	cfg := srvConfig{
		shutdownTimeout:             24 * time.Second,
		disableKeepAlivesOnShutdown: true,
	}

	for _, f := range options {
		f(&cfg)
	}

	if err := cfg.validate(); err != nil {
		return nil, errors.Join(ErrBadSrvConfig, err)
	}

	return newServer(cfg)
}

func newServer(cfg srvConfig) (*Server, error) {

	hs := cfg.server
	handler := hs.Handler
	logf := cfg.logf

	// decorate the server handler with the "before handler middlewares" that
	// need to be as close to the handler implementations as possible
	{
		if !cfg.disableLogfMiddleware {
			mw := MiddlewareAddLoggerFactoryToContext(logf)
			handler = mw(handler)
		}

		handler = MiddlewareChainExt(cfg.beforeHandlerMiddlewares).Handler(handler)
	}

	var rs RequestShedder
	if cfg.requestShedderIsSet {
		rs = cfg.requestShedder
	} else if cfg.shedRequestsOnShutdown {
		rs = &defaultReqShedder{}
	}

	if rs != nil {
		mw := rs.ShedRequestMiddleware()
		handler = mw(handler)
	}

	// decorate the far end (closest to the client) of the already decorated
	// "server handler" with tracing capability middlewares
	hs.Handler = MiddlewareChainExt(cfg.traceMiddlewares).Handler(handler)

	return &Server{
		logf,
		nil,
		cfg.shutdownTimeout,
		rs,
		hs,
		cfg.shedRequestsOnShutdown,
		cfg.disableKeepAlivesOnShutdown,
	}, nil
}

func (srv *Server) Addr() string {
	return srv.server.Addr
}

func (srv *Server) ShutdownTimeout() time.Duration {
	return srv.shutdownTimeout
}

func (srv *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	srv.server.Handler.ServeHTTP(w, r)
}

// RegisterAfterShutdown is used to register dependency cleanup hooks that should
// run only after all critical transactions have completed ( under normal load circumstances )
//
// If there is any error shutting down ( such as a shutdown timeout ) the hooks will receive
// the error transparently so they can make context driven decisions if they need to.
func (srv *Server) RegisterAfterShutdown(f func(error)) {
	srv.afterShutdownFuncs = append(srv.afterShutdownFuncs, f)
}

// newDisposer returns a function that will run all the
// registered shutdown funcs concurrently once called.
//
// The caller will not receive control again until all goroutines finish.
func (srv *Server) newDisposer(ctx context.Context, logger xslog.Logger) func(err error) {

	afterShutdownFuncs := srv.afterShutdownFuncs
	srv.afterShutdownFuncs = nil

	count := len(afterShutdownFuncs)
	if count <= 0 {
		logger.Warn(ctx,
			"no cleanup process to run on shutdown",
		)

		return nil
	}

	logger.Warn(ctx,
		"have cleanup process to run on shutdown",
		slog.Int("count", count),
	)

	return func(err error) {
		var wg sync.WaitGroup

		logger.Warn(ctx,
			"starting cleanup process",
		)

		wg.Add(len(afterShutdownFuncs))
		for _, f := range afterShutdownFuncs {
			go func() {
				defer wg.Done()

				f(err)
			}()
		}

		logger.Warn(ctx,
			"waiting for cleanup process to finish",
		)

		wg.Wait()

		logger.Warn(ctx,
			"cleanup process done",
		)
	}
}

// Dispose does not need to be called if the server ever had ListenAndServe called.
//
// It should only be called at most once in the case where a server initialized partially
// but failed to get to a running state.
func (srv *Server) Dispose(ctx context.Context, err error) {
	logger := srv.Logger(ctx)

	if disposer := srv.newDisposer(ctx, logger); disposer != nil {
		disposer(err)
	}
}

func (srv *Server) ListenAndServe(ctx context.Context) (err error) {

	logger := srv.Logger(ctx)

	// defer after shutdown runner
	if disposer := srv.newDisposer(ctx, logger); disposer != nil {
		defer func() {
			err := err // intentionally shadowing err, the parent context value should not be mutated
			var recoverVal any

			if err == nil {
				if r := recover(); r != nil {
					recoverVal = r
					defer panic(r)

					switch v := r.(type) {
					case []byte:
						err = fmt.Errorf("%w: %s", ErrListenAndServePanic, string(v))
					case []rune:
						err = fmt.Errorf("%w: %s", ErrListenAndServePanic, string(v))
					case string:
						err = fmt.Errorf("%w: %s", ErrListenAndServePanic, v)
					case error:
						err = errors.Join(ErrListenAndServePanic, v)
					default:
						err = ErrListenAndServePanic
					}
				}
			}

			defer disposer(err)

			if recoverVal != nil {
				logger.Error(ctx,
					"http server panic",
					slog.Any("recover", recoverVal),
				)
			}
		}()
	}

	logger.Info(ctx, "starting http server",
		slog.String("addr", srv.server.Addr),
		slog.Int64("shutdown_timeout", int64(srv.shutdownTimeout)),
		slog.Bool("keepalive_disabled_on_shutdown", srv.disableKeepAlivesOnShutdown),
	)

	err = srv.listenAndServe(ctx, logger)
	return
}

func splitAndValidateHostPort(addr string) (string, int, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, fmt.Errorf("server address is malformed for net.SplitHostPort: %w", err)
	}

	port, err := strconv.Atoi(portStr)
	if err != nil {
		return "", 0, fmt.Errorf("server address port segment is malformed: %w", err)
	}

	const maxValidPortValue = int(math.MaxUint16)
	if port < 0 || port > maxValidPortValue {
		return "", 0, errors.New("server address port must be within the inclusive range of 0 to " + strconv.Itoa(maxValidPortValue))
	}

	return host, port, nil
}

func (srv *Server) listenAndServe(ctx context.Context, logger xslog.Logger) error {
	const badSrvConfEvt = "bad server config"

	if srv.server.Handler == nil {
		err := ErrNilSrvHandler

		logger.WithErr(ctx, err).Error(ctx, badSrvConfEvt)
		return errors.Join(ErrBadSrvConfig, err)
	}

	var addr string
	var port int
	{
		host, portVal, err := splitAndValidateHostPort(srv.server.Addr)
		if err != nil {
			logger.WithErr(ctx, err).Error(ctx, badSrvConfEvt)
			return errors.Join(ErrBadSrvConfig, err)
		}

		port = portVal
		addr = net.JoinHostPort(host, strconv.Itoa(portVal))
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		// "failed to listen on tcp address"
		logger.WithErr(ctx, err).Error(ctx,
			"failed to create listener",
		)
		return errors.Join(ErrServeFailed, err)
	}
	defer listener.Close()

	if port == 0 {
		logger.Warn(ctx,
			"listening on random port",
			slog.String("addr", listener.Addr().String()),
		)
	}

	errChan := make(chan error, 1)
	cancelStopper := make(chan struct{})
	ctxDone := ctx.Done()

	// server stopper
	//
	// note this function must only ever attempt to write to errChan once
	// no more, no less
	go func() {
		defer close(errChan)

		select {
		case <-cancelStopper:
			errChan <- nil
			return
		case <-ctxDone:
			// continue with shutdown process
		}

		logger.Warn(ctx,
			"graceful server shutdown requested",
		)

		if srv.disableKeepAlivesOnShutdown {
			srv.server.SetKeepAlivesEnabled(false)
		}

		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), srv.shutdownTimeout)
		defer cancel()

		if srv.shedRequestsOnShutdown {
			rs := srv.requestShedder

			shutdownTimeoutChan := ctx.Done()
			handlersDone := make(chan struct{})

			go func() {
				rs.ShedRequests(ctx)

				close(handlersDone)
			}()

			select {
			case <-shutdownTimeoutChan:
				// context has reached a steady done state, so using
				// context.Cause instead of xcontext.Cause is acceptable
				err := context.Cause(ctx)
				errChan <- err

				logger.WithErr(ctx, err).Error(ctx,
					"failed to shed requests within the shutdown timeout",
				)

				// calling shutdown just so it can be known we tried
				//
				// error intentionally ignored so the result of xcontext.Cause(ctx) can be returned
				_ = srv.server.Shutdown(ctx) // nolint:errcheck

				return
			case <-handlersDone:
				// all handlers finished writing their transaction results
				//
				// continue
			}
		}

		errChan <- srv.server.Shutdown(ctx)
	}()

	//
	// gather signal to determine success
	// of the overall lifecycle and ensure
	// routines are cleaned up
	//

	serveErr := srv.server.Serve(listener)
	close(cancelStopper)

	shutdownErr, ok := <-errChan
	if !ok {
		panic("no signal from shutdown goroutine")
	}

	defer func() {
		// a final read to ensure the routine has fully returned
		<-errChan
	}()

	//
	// Now process error information, log accordingly, and return
	//

	if serveErr != nil {
		if !errors.Is(serveErr, http.ErrServerClosed) {
			logger.WithErr(ctx, serveErr).Error(ctx, "http server failed to serve")

			serveErr = fmt.Errorf("%w: %w", ErrServeFailed, serveErr)
			if shutdownErr == nil {
				return serveErr
			}
		} else {
			serveErr = nil
		}
	}

	if shutdownErr != nil {
		if shutdownErr == context.DeadlineExceeded {
			shutdownErr = ErrShutdownDeadlineExceeded
		}
		logger.WithErr(ctx, shutdownErr).Error(ctx, "http server failed to gracefully shutdown")

		shutdownErr = fmt.Errorf("%w: %w", ErrShutdownFailed, shutdownErr)
		if serveErr == nil {
			return shutdownErr
		}

		// have both a shutdown error and a serve error
		return errors.Join(serveErr, shutdownErr)
	}

	logger.Warn(ctx, "http server gracefully shutdown")

	return nil
}
