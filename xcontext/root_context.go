package xcontext

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/ballastworks/xs/xlog/xslog"
)

const (
	shutdownLogEvtName  = "shutdown requested"
	shutdownLogTypeName = "signaler"
)

var (
	// notShutdown indicates either a successful run or a panic
	//
	// not a shutdown signal
	errNotShutdown = errors.New("not shutdown")
)

type processShutdownSignalErr struct {
	s os.Signal
}

func (ps processShutdownSignalErr) Error() string {
	return "process shutdown signal received: " + ps.s.String()
}

// WithRootShutdownSignalContext creates a root context that listens for OS shutdown signals (SIGINT, SIGTERM).
// When such a signal is received, the context is cancelled with an error indicating the signal received.
// The provided function `f` is called with the created context, a cancel function, and a logger setter function.
// The logger setter function allows setting a logger that will be used to log shutdown events and panics.
//
// Constructing a logger and calling the passed in setRCLogger function with it as soon as possible is recommended and should the logging configuration be changed again to construct a new logger then call setRCLogger again with the new logger. If the logger is nil, a panic will occur.
func WithRootShutdownSignalContext(f func(ctx context.Context, cancel context.CancelCauseFunc, setRCLogger func(xslog.Logger))) {
	procDone := make(chan os.Signal, 1)
	signal.Notify(procDone, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)

	var setLogger func(xslog.Logger)
	var getLogger func() xslog.Logger
	{
		type someLogger struct{ v xslog.Logger }

		var logger atomic.Value
		logger.Store(someLogger{nil})

		setLogger = func(slog xslog.Logger) {
			if slog == nil {
				panic("nil logger")
			}

			logger.Store(someLogger{slog})
		}
		getLogger = func() xslog.Logger {
			return logger.Load().(someLogger).v
		}
	}

	ctx, cancel := context.WithCancelCause(context.Background())

	var wg sync.WaitGroup
	wg.Go(func() {
		done := ctx.Done()

		select {
		case s := <-procDone:
			cancel(processShutdownSignalErr{s})
		case <-done:
		}

		logger := getLogger()
		if logger == nil {
			return
		}

		c := context.Cause(ctx)
		if c == errNotShutdown {
			return
		}

		if sigErr, ok := c.(processShutdownSignalErr); ok {
			logger.Warn(ctx,
				shutdownLogEvtName,
				slog.String(shutdownLogTypeName, "process"),
				slog.String("signal", sigErr.s.String()),
			)
			return
		}

		logger.Warn(ctx,
			shutdownLogEvtName,
			slog.String(shutdownLogTypeName, "context"),
			slog.Any("error", ctx.Err()),
			slog.Any("cause", c),
		)
	})

	defer wg.Wait()
	defer signal.Stop(procDone)
	defer cancel(errNotShutdown)

	defer func() {
		logger := getLogger()
		if logger == nil {
			return
		}

		r := recover()
		if r == nil {
			return
		}
		defer panic(r)

		logger.Error(ctx,
			"root context panic",
			slog.Any("error", r),
		)
	}()

	f(ctx, cancel, setLogger)
}
