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

// addErrAttrs is the same as errAttrs except it adds the error attributes
// directly to the record rather than allocating a slice to hold them.
func addErrAttrs(r *slog.Record, err error) {
	if v := xerrors.Stacktrace(err); v != nil {
		r.AddAttrs(slog.Any(ErrorLogKey, err), slog.String(StacktraceLogKey, v.String()))
		return
	}

	r.AddAttrs(slog.Any(ErrorLogKey, err))
}
