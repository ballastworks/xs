package xhttp

import (
	"errors"
	"net/http"

	"github.com/ballastworks/xs/xlog/xslog"
)

type routerConfig struct {
	router                  Router
	logf                    xslog.LoggerFactory
	errHandlerStrategy      func(errHandlerFunc) http.Handler
	panicHandler            func(w http.ResponseWriter, r *http.Request, rec any)
	logfSet                 bool
	errHandlerStrategyIsSet bool
	panicHandlerIsSet       bool
	routerIsSet             bool
	ignoreTrailingSlash     bool
}

func (cfg *routerConfig) validate() error {
	if cfg.routerIsSet && cfg.router == nil {
		return errors.New("nil router specified")
	}

	if cfg.panicHandlerIsSet && cfg.panicHandler == nil {
		return errors.New("nil panic handler specified")
	}

	if !cfg.logfSet {
		cfg.logf = DefaultLoggerFactory()
	} else if cfg.logf == nil {
		return errors.New("nil logger factory specified")
	}

	if !cfg.routerIsSet {
		rt := newDefaultRouter()
		if cfg.panicHandlerIsSet {
			rt.PanicHandler = cfg.panicHandler
		}
		cfg.router = rt
	}

	return nil
}

type RouterOption func(*routerConfig)

type routerOpts struct {
}

func RouterOpts() routerOpts {
	return routerOpts{}
}

func (routerOpts) LoggerFactory(logf xslog.LoggerFactory) RouterOption {
	return func(cfg *routerConfig) {
		cfg.logf = logf
		cfg.logfSet = true
	}
}

func (routerOpts) ErrHandler(f func(errHandlerFunc) http.Handler) RouterOption {
	return func(cfg *routerConfig) {
		cfg.errHandlerStrategy = f
		cfg.errHandlerStrategyIsSet = true
	}
}

func (routerOpts) IgnoreTrailingSlash(b bool) RouterOption {
	return func(cfg *routerConfig) {
		cfg.ignoreTrailingSlash = b
	}
}

func (routerOpts) PanicHandler(f func(w http.ResponseWriter, r *http.Request, rec any)) RouterOption {
	return func(cfg *routerConfig) {
		cfg.panicHandler = f
		cfg.panicHandlerIsSet = true
	}
}

func (routerOpts) Router(r Router) RouterOption {
	return func(cfg *routerConfig) {
		cfg.router = r
		cfg.routerIsSet = true
	}
}
