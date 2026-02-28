package xhttp

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/ballastworks/xs/xlog/xslog"
)

type RequestShedder interface {
	// RequestShedder is exported because it is used as an argument type for the SrvOpts.RequestShedder option

	ShedRequestMiddleware() func(next http.Handler) http.Handler
	ShedRequests(context.Context)
}

type srvConfig struct {
	server                      *http.Server
	traceMiddlewares            MiddlewareChain
	beforeHandlerMiddlewares    MiddlewareChain
	shutdownTimeout             time.Duration
	requestShedder              RequestShedder
	logf                        xslog.LoggerFactory
	shedRequestsOnShutdown      bool
	serverSet                   bool
	requestShedderIsSet         bool
	traceMiddlewaresSet         bool
	beforeHandlerMiddlewaresSet bool
	disableKeepAlivesOnShutdown bool
	logfSet                     bool
	disableLogfMiddleware       bool
}

func (cfg *srvConfig) validate() error {
	if !cfg.logfSet {
		cfg.logf = DefaultLoggerFactory()
	} else if cfg.logf == nil {
		return errors.New("nil logger factory specified")
	}

	if cfg.requestShedderIsSet && cfg.requestShedder == nil {
		return errors.New("nil request shedder specified")
	}

	if cfg.serverSet && cfg.server == nil {
		return errors.New("nil server specified")
	} else if !cfg.serverSet {
		return errors.New("HTTP Server must be specified")
	}

	if cfg.server.Handler == nil {
		return ErrNilSrvHandler
	}

	// This check is intentionally duplicated because the caller can mutate the
	// .Addr attribute after init time.
	if _, _, err := splitAndValidateHostPort(cfg.server.Addr); err != nil {
		return err
	}

	//
	// defaults
	//

	if !cfg.beforeHandlerMiddlewaresSet {
		cfg.beforeHandlerMiddlewares = DefaultBeforeHandlerMiddlewareChain(cfg.logf)
	}

	if !cfg.traceMiddlewaresSet {
		cfg.traceMiddlewares = DefaultTraceMiddlewareChain()
	}

	return nil
}

type srvOpts struct {
}

type SrvOption func(*srvConfig)

func SrvOpts() srvOpts {
	return srvOpts{}
}

func (srvOpts) Server(s *http.Server) SrvOption {
	return func(cfg *srvConfig) {
		cfg.server = s
		cfg.serverSet = true
	}
}

func (srvOpts) TraceMiddlewares(tm ...Middleware) SrvOption {
	return func(cfg *srvConfig) {
		cfg.traceMiddlewares = NewMiddlewareChain(tm...)
		cfg.traceMiddlewaresSet = true
	}
}

func (srvOpts) BeforeHandlerMiddlewares(bhm ...Middleware) SrvOption {
	return func(cfg *srvConfig) {
		cfg.beforeHandlerMiddlewares = NewMiddlewareChain(bhm...)
		cfg.beforeHandlerMiddlewaresSet = true
	}
}

func (srvOpts) ShutdownTimeout(d time.Duration) SrvOption {
	return func(cfg *srvConfig) {
		cfg.shutdownTimeout = d
	}
}

func (srvOpts) ShedRequestsOnShutdown(b bool) SrvOption {
	return func(cfg *srvConfig) {
		cfg.shedRequestsOnShutdown = b
	}
}

func (srvOpts) DisableKeepAlivesOnShutdown(b bool) SrvOption {
	return func(cfg *srvConfig) {
		cfg.disableKeepAlivesOnShutdown = b
	}
}

func (srvOpts) DisableLoggerFactoryMiddleware(b bool) SrvOption {
	return func(cfg *srvConfig) {
		cfg.disableLogfMiddleware = b
	}
}

// RequestShedder will not have an effect unless
// ShedRequestsOnShutdown is set to true
func (srvOpts) RequestShedder(rs RequestShedder) SrvOption {
	return func(cfg *srvConfig) {
		cfg.requestShedderIsSet = true
		cfg.requestShedder = rs
	}
}

func (srvOpts) LoggerFactory(factory xslog.LoggerFactory) SrvOption {
	return func(cfg *srvConfig) {
		cfg.logf = factory
		cfg.logfSet = true
	}
}
