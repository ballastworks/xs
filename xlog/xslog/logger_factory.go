package xslog

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/ballastworks/xs/xsync/xrwm"
)

const defaultNewLoggerLevel = slog.LevelInfo

var (
	defaultLogLevelEnvKeySearch = []string{"DEFAULT_LOG_LEVEL", "LOG_LEVEL"}
	newLogLevelEnvKeySearch     = []string{"LOG_LEVEL", "DEFAULT_LOG_LEVEL"}
)

var defaultLogger, _ = newEnvLogger(false, defaultLogLevelEnvKeySearch...)

func newEnvLogger(returnParseErrors bool, logLevelKeySearch ...string) (*structLogger, error) {

	logLevel := defaultNewLoggerLevel
	{
		var level slog.Level

		for _, k := range logLevelKeySearch {
			logLevelStr, ok := os.LookupEnv(k)
			if !ok {
				continue
			}

			if logLevelStr = strings.TrimSpace(logLevelStr); logLevelStr != "" {
				if err := level.UnmarshalText([]byte(logLevelStr)); err == nil {
					logLevel = level
					break
				}
				if returnParseErrors {
					return nil, errors.New("could not parse log level value in environment variable " + k)
				}
			}
		}
	}

	logger, err := New(LoggerOpts().Level(logLevel))
	if err != nil {
		return nil, err
	}

	return logger.(*structLogger), nil
}

func newEnvStructLogger() *structLogger {
	logger, _ := newEnvLogger(false, newLogLevelEnvKeySearch...)
	return logger
}

func NewEnvLogger() (Logger, error) {
	return newEnvLogger(true, newLogLevelEnvKeySearch...)
}

// L returns a default logger initialized at package init time
// without any context information.
//
// In general this logger should be avoided in high stake service apps.
// It exists only for use in quick scripts.
//
// The log level is set dynamically based on the value of the
// DEFAULT_LOG_LEVEL env variable if defined. It not then it
// will try LOG_LEVEL
//
// Note that the env loading order is swapped from NewEnvLogger and
// NewFactory's default loggerFunc strategy on purpose in case
// the default logger statements need to be silenced in an application.
func L() Logger {
	return defaultLogger
}

func checkNewEnvLogger(logger *structLogger) {
	if logger != nil {
		return
	}

	panic("nil logger from newEnvStructLogger")
}

func checkLoadedEnvLoggerFactory(v LoggerFactory) {
	if v != nil {
		return
	}

	//
	// if a panic occurs here then the logic that initializes
	// the cached env logger has failed somehow.
	//
	// It would only fail if newEnvStructLogger panics which
	// should never happen. So if that holds true then the only
	// alternative is a larger system memory corruption issue.
	//

	panic("cached env logger is not initialized")
}

func newCachedEnvLoggerFactoryResolver() func() (LoggerFactory, error) {

	var factory atomic.Value
	factory.Store(boxedLoggerFactory{nil})
	var initLock atomic.Bool
	var mu sync.Mutex     // only taken on the slow-path (first wave ideally)
	var wg sync.WaitGroup // wg is a semaphore barrier: Add(1) once, Done() once

	return func() (LoggerFactory, error) {
		if v := factory.Load().(boxedLoggerFactory).v; v != nil {
			// Fast path: already initialized.
			return v, nil
		}

		if !initLock.Load() {
			mu.Lock()
			st := xrwm.WLocked
			defer func() {
				switch st {
				case xrwm.WLocked:
					mu.Unlock()
				}
			}()

			if !initLock.Load() {
				wg.Add(1)
				initLock.Store(true)
				mu.Unlock()
				st = xrwm.Unlocked

				// using a defer because we're just returning after the atomic
				// store operation
				defer wg.Done()

				// This block has been entered because the initLock value has for
				// the first and only time was switched from false to true. This
				// means that this is the first goroutine to reach this point of
				// the code, and it is responsible for initializing the cached
				// environment logger. The wait group is guaranteed to be finished
				// after this block finishes (i.e. when return is called).

				logger := newEnvStructLogger()
				checkNewEnvLogger(logger)

				sf := staticFactory{logger}
				factory.Store(boxedLoggerFactory{sf})

				return sf, nil
			}
		}

		// If a program has reached this point, it means that it is waiting on
		// another concurrent routine to finish initializing the cached
		// environment logger. So the first step is to wait for that
		// synchronous signal then load the cached value we need to return.

		wg.Wait()

		v := factory.Load().(boxedLoggerFactory).v
		checkLoadedEnvLoggerFactory(v)

		return v, nil
	}
}

var cachedEnvLoggerFactoryResolver = newCachedEnvLoggerFactoryResolver()

type staticFactory struct {
	logger Logger
}

func (sf staticFactory) Enabled(ctx context.Context, level slog.Level) bool {
	return sf.logger.Enabled(ctx, level)
}

func (sf staticFactory) Logger(context.Context) Logger {
	return sf.logger
}

// StaticFactory converts a Logger implementation into a Logger Factory.
//
// The result implements LoggerFactory.
func StaticFactory(logger Logger) staticFactory {
	if logger == nil {
		panic("cannot create a log factory from a nil Logger")
	}

	return staticFactory{logger}
}

func NewFactory(options ...LoggerFactoryOption) (LoggerFactory, error) {

	cfg := loggerFactoryConfig{
		loggerFactoryResolver: cachedEnvLoggerFactoryResolver,
	}

	for _, f := range options {
		f(&cfg)
	}

	if err := cfg.validate(); err != nil {
		return nil, errors.Join(ErrBadLoggerFactoryConfig, err)
	}

	if v := cfg.logger; v != nil {
		return staticFactory{v}, nil
	}

	if fr := cfg.loggerFactoryResolver; fr != nil {
		return fr()
	}

	return cfg.loggerFactory, nil
}
