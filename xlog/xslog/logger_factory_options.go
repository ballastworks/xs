package xslog

import (
	"errors"
)

var (
	ErrBadLoggerFactoryConfig = errors.New("bad logger factory config")
)

type loggerFactoryConfig struct {
	logger                   Logger
	loggerFactoryResolver    func() (LoggerFactory, error)
	loggerSet                bool
	loggerFactoryResolverSet bool
}

func (cfg *loggerFactoryConfig) validate() error {
	if cfg.loggerSet && cfg.loggerFactoryResolverSet {
		// note that since there is a default non-nil value set for the factory resolver neither needs to be set
		return errors.New("must not specify both LoggerFactoryResolver and Logger options")
	}

	if cfg.loggerSet && cfg.logger == nil {
		return errors.New("nil logger specified")
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
