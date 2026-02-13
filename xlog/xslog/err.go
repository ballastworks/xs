package xslog

import (
	"log/slog"

	"github.com/ballastworks/xs/xerrors"
)

const ErrorLogKey = "error"
const StacktraceLogKey = "error.trace"

func errAttrs(err error) []slog.Attr {
	if v := xerrors.Stacktrace(err); v != nil {
		return []slog.Attr{slog.Any(ErrorLogKey, err), slog.String(StacktraceLogKey, v.String())}
	}

	return []slog.Attr{slog.Any(ErrorLogKey, err)}
}
