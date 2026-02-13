package xslog

import (
	"errors"
)

var (
	ErrBadLoggerFactoryConfig = errors.New("bad logger factory config")
)

type loggerFactoryConfig struct {
	logger                   Logger
	loggerFactory            LoggerFactory
	loggerFactoryResolver    func() (LoggerFactory, error)
	loggerSet                bool
	loggerFactorySet         bool
	loggerFactoryResolverSet bool
}

func (cfg *loggerFactoryConfig) validate() error {
	var numSet uint8

	if cfg.loggerSet {
		numSet++
	}
	if cfg.loggerFactorySet {
		numSet++
	}
	if cfg.loggerFactoryResolverSet {
		numSet++
	}

	if numSet > 1 {
		// note that since there is a default non-nil value set for the factory resolver numSet is allowed to be zero
		return errors.New("must not specify more than one of LoggerFactoryResolver, LoggerFactory, Logger options")
	}

	if cfg.loggerSet && cfg.logger == nil {
		return errors.New("nil logger specified")
	}

	if cfg.loggerFactorySet && cfg.loggerFactory == nil {
		return errors.New("nil logger factory specified")
	}

	if cfg.loggerFactoryResolverSet && cfg.loggerFactoryResolver == nil {
		return errors.New("nil logger factory resolver specified")
	}

	return nil
}

type LoggerFactoryOption func(*loggerFactoryConfig)

type loggerFactoryOpts struct{}

func LoggerFactoryOpts() loggerFactoryOpts {
	return loggerFactoryOpts{}
}

func (loggerFactoryOpts) LoggerFactory(factory LoggerFactory) LoggerFactoryOption {
	return func(cfg *loggerFactoryConfig) {
		cfg.loggerFactory = factory
		cfg.loggerFactorySet = true
	}
}

func (loggerFactoryOpts) Logger(logger Logger) LoggerFactoryOption {
	return func(cfg *loggerFactoryConfig) {
		cfg.logger = logger
		cfg.loggerSet = true
	}
}

func (loggerFactoryOpts) LoggerFactoryResolver(fr func() (LoggerFactory, error)) LoggerFactoryOption {
	return func(cfg *loggerFactoryConfig) {
		cfg.loggerFactoryResolver = fr
		cfg.loggerFactoryResolverSet = true
	}
}
