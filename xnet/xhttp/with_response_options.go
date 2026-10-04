package xhttp

import (
	"context"

	"github.com/ballastworks/xs/internal/ctx_slog"
	"github.com/ballastworks/xs/xlog/xslog"
)

type withRespConfig struct {
	loggerDisabled bool
	logf           xslog.LoggerFactory
}

type WithRespOption func(*withRespConfig)

// loggerFactory returns the logger factory a response logs through when it is
// rendered by the response factory rf in ctx. In order of precedence:
//   - a nop factory when logging is disabled
//   - the response's own LoggerFactory option
//   - rf's LoggerFactory option, when it was configured
//   - the logger factory in ctx, such as the one a Server or Router adds
//   - the default xhttp logger factory
func (cfg withRespConfig) loggerFactory(ctx context.Context, rf *ResponseFactory) xslog.LoggerFactory {
	if cfg.loggerDisabled {
		return xslog.NopFactory()
	}

	if logf := cfg.logf; logf != nil {
		return logf
	}

	if rf.logfSet {
		return rf.LoggerFactory
	}

	if v := ctx_slog.LoggerFactoryFromContext(ctx); v != nil {
		return v.(xslog.LoggerFactory)
	}

	return DefaultLoggerFactory()
}

func (cfg withRespConfig) with(options ...WithRespOption) withRespConfig {

	for _, f := range options {
		f(&cfg)
	}

	return cfg
}

type withRespOpts struct {
}

func WithRespOpts() withRespOpts {
	return withRespOpts{}
}

func (withRespOpts) LoggerDisabled(b bool) WithRespOption {
	return func(cfg *withRespConfig) {
		cfg.loggerDisabled = b
	}
}

func (withRespOpts) LoggerFactory(logf xslog.LoggerFactory) WithRespOption {
	return func(cfg *withRespConfig) {
		cfg.logf = logf
	}
}

type withErrRespConfig struct {
	withRespConfig
	failSpan bool
}

type WithErrRespOption func(*withErrRespConfig)

func (cfg withErrRespConfig) with(options ...WithErrRespOption) withErrRespConfig {

	for _, f := range options {
		f(&cfg)
	}

	return cfg
}

type withErrRespOpts struct {
}

func WithErrRespOpts() withErrRespOpts {
	return withErrRespOpts{}
}

func (withErrRespOpts) LoggerDisabled(b bool) WithErrRespOption {
	return func(cfg *withErrRespConfig) {
		cfg.loggerDisabled = b
	}
}

func (withErrRespOpts) FailSpan(b bool) WithErrRespOption {
	return func(cfg *withErrRespConfig) {
		cfg.failSpan = b
	}
}

func (withErrRespOpts) LoggerFactory(logf xslog.LoggerFactory) WithErrRespOption {
	return func(cfg *withErrRespConfig) {
		cfg.logf = logf
	}
}
