package xi_errors

import (
	"errors"
)

func StacktraceFromError(err error) interface{ Stacktrace() []uintptr } {
	//
	// errors.As handles both linear unwrap chains and multi-errors (e.g., errors.Join).
	//
	// It might be less than desirable to traverse multi-error unwraps here,
	// but it's a best effort affair which is likely what the caller would want.
	//
	// Should we collectively want to avoid multi-error traversal here,
	// we can add construction-time options to WithStack and New to
	// indicate that behavior.
	//

	type stackTracer interface {
		Stacktrace() []uintptr
	}

	t := stackTracer(nil)

	if !errors.As(err, &t) || t == nil {
		return nil
	}

	for {

		//
		// Intentionally not traversing `Unwrap() []errors` implementations here
		// as errors.As already handles that case at the above level, but more
		// importantly the tracedError type which is what we're intending to peek
		// within here at this context only implements `Unwrap() error`.
		//

		// Peek within the error to see if the error implementing the tracer
		// wraps around another error implementing the tracer, so that the
		// innermost tracer is returned: the one closest to where the error
		// happened. A tracer holding no frames is skipped over, though, and
		// the search stops at the tracer above it: that inner error has been
		// released (a shared error returned once already) and the outer
		// tracer is the fresh stack WithStack took for this occurrence.
		if v, ok := t.(interface{ Unwrap() error }); ok {
			if err := v.Unwrap(); err != nil {
				if nt := stackTracer(nil); errors.As(err, &nt) && nt != nil && len(nt.Stacktrace()) > 0 {
					t = nt
					continue
				}
			}
		}

		break
	}

	return t
}
