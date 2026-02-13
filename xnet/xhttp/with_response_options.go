package xhttp

import (
	"github.com/ballastworks/xs/xlog/xslog"
)

type withRespConfig struct {
	loggerDisabled bool
	logf           xslog.LoggerFactory
}

type WithRespOption func(*withRespConfig)

func (cfg withRespConfig) loggerFactory() xslog.LoggerFactory {
	if cfg.loggerDisabled {
		return xslog.NopFactory()
	}

	if logf := cfg.logf; logf != nil {
		return logf
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
