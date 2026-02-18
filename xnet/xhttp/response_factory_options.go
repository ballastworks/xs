package xhttp

import (
	"context"
	"errors"
	"log/slog"

	"github.com/ballastworks/xs/xerrors"
	"github.com/ballastworks/xs/xlog/xslog"
)

func DefaultErrRespLoggingFunc(ctx context.Context, logger xslog.Logger, err error, statusCode int) {
	const msg = "error http response"

	var level slog.Level
	var includeRemediation bool

	switch statusCode {
	case 400:
		// likely a garbage request from a bad client
		//
		// if your server is exposed to the internet this may be too noisy
		level = slog.LevelWarn
	case 404:
		// likely a garbage request from a bad client
		//
		// if your server is NOT exposed to the internet this likely is not noisy enough
		level = slog.LevelDebug
	case 499:
		// client closed the connection ( most likely )
		//
		// no reason to be noisy about it

		// TODO: there is likely some time threshold at which to treat these as warning level vs debug level

		level = slog.LevelDebug
	case 500:
		// an internal server error occurred
		//
		// client should try their request again later
		level = slog.LevelError
	case 503:
		// request received while shutting down ( most likely )
		level = slog.LevelError
	case 504:
		// some internal timeout was reached and client should likely try again later
		level = slog.LevelError
	default:
		if statusCode >= 400 {

			if statusCode < 500 {
				// [400, 500) range
				level = slog.LevelWarn
			} else if statusCode < 600 {
				// [500, 600) range
				level = slog.LevelError
			} else {
				// would be greater than 600
				//
				// not a valid response code range to use at all
				level = slog.LevelInfo
				includeRemediation = true
			}

		} else if statusCode >= 100 {
			// [100, 399] range
			//
			// not an error response code, should have never called this function
			level = slog.LevelInfo
			includeRemediation = true
		} else {
			// would be less than 100
			//
			// not a valid response code range, should never have used this code
			level = slog.LevelInfo
			includeRemediation = true
		}
	}

	// // Utilizes:
	// // - "go.opentelemetry.io/otel/codes"
	// // - sdktrace "go.opentelemetry.io/otel/sdk/trace"
	// // - "go.opentelemetry.io/otel/trace"
	// //
	// // attempt to fail the span iff not already have a status set
	// if level >= slog.LevelError {
	// 	if span := trace.SpanFromContext(ctx); span.IsRecording() {
	// 		if v, ok := span.(sdktrace.ReadWriteSpan); ok {
	// 			if v.Status().Code == codes.Unset {
	// 				span.SetStatus(codes.Error, msg+": "+cause.Error())
	// 			}
	// 		}
	// 	}
	// }

	if !logger.Enabled(ctx, level) {
		return
	}

	// key formats: https://opentelemetry.io/docs/specs/semconv/registry/attributes/http/

	attrsBuf := [5]slog.Attr{}
	attrs := append(attrsBuf[:0], slog.Int("http.response.status_code", statusCode))

	var dmsgr interface{ DevMsg() string }
	cause := err

	if errRespr, ok := err.(errResponder); ok {
		if errCause := errRespr.Unwrap(); errCause != nil {
			cause = errCause
			if v, ok := cause.(interface{ DevMsg() string }); ok {
				dmsgr = v
			}
		}
	}

	if dmsgr == nil {
		if v, ok := err.(interface{ DevMsg() string }); ok {
			dmsgr = v
		}
	}

	if dmsgr != nil {
		attrs = append(attrs, slog.String("x.http.response.err.devmsg", dmsgr.DevMsg()))
	}

	attrs = append(attrs, slog.Any("x.http.response.err.cause", cause))

	if v := xerrors.Stacktrace(cause); v != nil {
		attrs = append(attrs, slog.String("code.stacktrace", v.String()))
	}

	if includeRemediation {
		attrs = append(attrs, slog.String("remediation_hint", "don't use this value in any response code"))
	}

	logger.LogUnchecked(ctx, level, msg, attrs...)
}

type ErrRespLoggingFunc func(ctx context.Context, logger xslog.Logger, err error, statusCode int)

type responseFactoryConfig struct {
	logf                  xslog.LoggerFactory
	errRespLoggingFunc    ErrRespLoggingFunc
	logfSet               bool
	errRespLoggingFuncSet bool
}

func (cfg *responseFactoryConfig) validate() error {
	if !cfg.logfSet {
		cfg.logf = defaultLoggerFactory
	} else if cfg.logf == nil {
		return errors.New("nil logger factory specified")
	}

	if cfg.errRespLoggingFuncSet && cfg.errRespLoggingFunc == nil {
		return errors.New("nil error response logging function specified")
	}

	return nil
}

type ResponseFactoryOption func(*responseFactoryConfig)

type responseFactoryOpts struct{}

func ResponseFactoryOpts() responseFactoryOpts {
	return responseFactoryOpts{}
}

func (responseFactoryOpts) LoggerFactory(logf xslog.LoggerFactory) ResponseFactoryOption {
	return func(cfg *responseFactoryConfig) {
		cfg.logf = logf
		cfg.logfSet = true
	}
}

func (responseFactoryOpts) ErrRespLoggingFunc(f ErrRespLoggingFunc) ResponseFactoryOption {
	return func(cfg *responseFactoryConfig) {
		cfg.errRespLoggingFunc = f
		cfg.errRespLoggingFuncSet = true
	}
}
