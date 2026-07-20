// Package xerrors provides error values that can carry a captured call stack.
//
// Formatting:
//   - %v  prints the error message (like the standard library)
//   - %+v prints the error message followed by a stack trace
package xerrors

import (
	"errors"
	"fmt"
	"io"
	"math/bits"
	"runtime"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/ballastworks/xs/internal/xi_errors"
)

const (
	sentinelFunctionName = "runtime.goexit"

	defaultStackDepthShift = 8
	defaultStackDepth      = (1 << defaultStackDepthShift)
	maxStackDepthShift     = 10
	tracePoolSize          = maxStackDepthShift - defaultStackDepthShift + 1
)

type tracer interface {
	Stacktrace() []uintptr
	ReleaseStacktrace()
}

type StacktraceLease = tracer

type stackTrace []uintptr

// Format writes the stack trace in a consistent, readable form.
func (s stackTrace) Format(w io.Writer) {
	if len(s) == 0 {
		return
	}

	s.format(w)
}

func (s stackTrace) format(w io.Writer) {
	frames := runtime.CallersFrames(s)

	for {
		fr, more := frames.Next()

		if more || fr.Function != sentinelFunctionName {

			fmt.Fprintf(w, "\t%s:%d\n\t\t%s\n", fr.File, fr.Line, fr.Function)

			if more {
				continue
			}
		}

		break
	}
}

// decLenU64 returns the number of bytes required to base 10 encode n.
//
// sources:
// - https://lemire.me/blog/2021/05/28/computing-the-number-of-digits-of-an-integer-quickly/
// - https://theswissbay.ch/pdf/Gentoomen%20Library/Security/Addison%20Wesley%20-%20Hackers%20Delight%202002.pdf
func decLenU64(n uint64) int {
	if n == 0 {
		return 1
	}

	// Approximate floor(log10(n)) + 1 using bit-length, then correct.
	// 1233/4096 ≈ log10(2)
	d := ((bits.Len64(n) * 1233) >> 12) + 1 // in [1..20], at most off by 1
	if n < [...]uint64{
		0x0000000000000001, // 1
		0x000000000000000A, // 10
		0x0000000000000064, // 100
		0x00000000000003E8, // 1_000
		0x0000000000002710, // 10_000
		0x00000000000186A0, // 100_000
		0x00000000000F4240, // 1_000_000
		0x0000000000989680, // 10_000_000
		0x0000000005F5E100, // 100_000_000
		0x000000003B9ACA00, // 1_000_000_000
		0x00000002540BE400, // 10_000_000_000
		0x000000174876E800, // 100_000_000_000
		0x000000E8D4A51000, // 1_000_000_000_000
		0x000009184E72A000, // 10_000_000_000_000
		0x00005AF3107A4000, // 100_000_000_000_000
		0x00038D7EA4C68000, // 1_000_000_000_000_000
		0x002386F26FC10000, // 10_000_000_000_000_000
		0x016345785D8A0000, // 100_000_000_000_000_000
		0x0DE0B6B3A7640000, // 1_000_000_000_000_000_000
		0x8AC7230489E80000, // 10_000_000_000_000_000_000
	}[d-1] {
		d--
	}

	return d
}

func traceSize(trace []uintptr) int {
	frames := runtime.CallersFrames(trace)

	var sum int
	for {
		fr, more := frames.Next()

		if more || fr.Function != sentinelFunctionName {

			sum += 6 + len(fr.File) + len(fr.Function) + decLenU64(uint64(fr.Line))
			if sum < 0 {
				panic("stack trace size overflow")
			}

			if more {
				continue
			}
		}

		break
	}

	return sum
}

func appendTrace(p []byte, trace []uintptr) []byte {
	frames := runtime.CallersFrames(trace)

	for {
		fr, more := frames.Next()

		if more || fr.Function != sentinelFunctionName {

			// TODO: might be good to have frame filtering to ensure more of
			// the important details remain un-cluttered - more signal than
			// noise.

			// // filters out prolific HandlerFunc wrappers
			// if strings.HasSuffix(fr.File, "/src/net/http/server.go") && fr.Function == "net/http.HandlerFunc.ServeHTTP" {
			// 	if !more {
			// 		break
			// 	}
			// 	continue
			// }

			p = append(p, '\t')
			p = append(p, fr.File...)
			p = append(p, ':')
			p = strconv.AppendInt(p, int64(fr.Line), 10)
			p = append(p, "\n\t\t"...)
			p = append(p, fr.Function...)
			p = append(p, '\n')

			if more {
				continue
			}
		}

		break
	}

	return p
}

func (s stackTrace) AppendText(p []byte) ([]byte, error) {
	if len(s) != 0 {
		p = slices.Grow(p, traceSize(s))
		p = appendTrace(p, s)
	}

	return p, nil
}

type pooledTrace struct {
	trace []uintptr
	// pool is a CAS enabled pointer to a *sync.Pool type
	poolAtomic unsafe.Pointer
}

//go:noinline
func newPooledTrace(skip int) *pooledTrace {
	// runtime.Callers skip counts from the Callers call itself.
	// We add:
	//   +1 for runtime.Callers
	//   +1 for newPooledTrace
	skip += 2

	// TODO: Could keep previous trace around until overflow of the checked out
	// buffer is confirmed. Likely not worth the CPU cycles to avoid the edge
	// memory waste.

	var pt *pooledTrace
	for i := range tracePoolSize - 1 {
		pool := &tracePools[i]
		pt = pool.Get().(*pooledTrace)
		n := runtime.Callers(skip, pt.trace)

		if n < len(pt.trace) {
			if n == 0 {
				pool.Put(pt)
				return nil
			}

			pt.trace = pt.trace[:n]
			pt.poolAtomic = unsafe.Pointer(pool)
			return pt
		}

		pool.Put(pt)
	}

	pool := &tracePools[tracePoolSize-1]
	pt = pool.Get().(*pooledTrace)
	n := runtime.Callers(skip, pt.trace)

	pt.trace = pt.trace[:n]
	pt.poolAtomic = unsafe.Pointer(pool)
	return pt
}

func (pt *pooledTrace) Stacktrace() []uintptr {
	if pt == nil {
		return nil
	}

	return pt.trace
}

func (pt *pooledTrace) ReleaseStacktrace() {
	if pt == nil || atomic.LoadPointer(&pt.poolAtomic) == nil {
		return
	}

	pool := (*sync.Pool)(atomic.SwapPointer(&pt.poolAtomic, nil))
	if pool == nil {
		return
	}

	pt.trace = pt.trace[:cap(pt.trace)]

	pool.Put(pt)
}

func StacktraceReleaser(err error) interface{ ReleaseStacktrace() } {
	if err != nil {
		if t, ok := getTracer(err); ok {
			return t
		}
	}

	return nil
}

//go:noinline
func WithStack(err error) error {
	if err == nil {
		return nil
	}

	// instead of wrapping and returning a new tracedError,
	// just going to return the existing error if it already
	// has a tracer in its chain.
	if _, ok := getTracer(err); ok {
		return err
	}

	trace := newPooledTrace(1)

	return &tracedError{err, trace}
}

//go:noinline
func New(msg string) error {
	err := errors.New(msg)

	trace := newPooledTrace(1)

	return &tracedError{err, trace}
}

// tracedError can be constructed with either:
//   - a real *Stack (preferred), or
//   - a stackTraceProvider (useful for interoperability / reusing foreign stack printers).
//
// It implements:
//   - error
//   - fmt.Formatter
//   - Unwrap() error
//   - Stack() []uintptr
type tracedError struct {
	cause error

	tracer
}

func (e *tracedError) Error() string { return e.cause.Error() }

func (e *tracedError) Unwrap() error { return e.cause }

// Format implements fmt.Formatter.
//
// Conventions:
//   - %s, %q: print the Error() string
//   - %v:     print Error()
//   - %+v:    print Error() then a newline and the stack trace
func (e *tracedError) Format(f fmt.State, verb rune) {
	switch verb {
	case 'v':
		if f.Flag('+') {
			io.WriteString(f, e.Error())

			if t := e.tracer; t != nil {
				if trace := t.Stacktrace(); len(trace) > 0 {
					io.WriteString(f, "\n")
					stackTrace(trace).Format(f)
				}
			}
			return
		}
		io.WriteString(f, e.Error())
	case 's':
		io.WriteString(f, e.Error())
	case 'q':
		fmt.Fprintf(f, "%q", e.Error())
	default:
		io.WriteString(f, e.Error())
	}
}

func (e *tracedError) AppendText(p []byte) ([]byte, error) {
	const joinStr = "\n"

	errMsg := e.Error()
	n := len(errMsg)

	var nTrace int
	if t := e.tracer; t != nil {
		if trace := t.Stacktrace(); len(trace) > 0 {
			nTrace = traceSize(trace)
			n += len(joinStr) + nTrace
		}
	}

	p = slices.Grow(p, n)
	p = append(p, errMsg...)
	if nTrace > 0 {
		p = append(p, joinStr...)
		p = appendTrace(p, e.tracer.Stacktrace())
	}

	return p, nil
}

//
// global table initializations
//

var tracePools [tracePoolSize]sync.Pool

func init() {
	for i := range tracePools {
		size := defaultStackDepth << uint(i)
		tracePools[i].New = func() any {
			trace := make([]uintptr, size)
			return &pooledTrace{trace, nil}
		}
	}
}

//
// helpers
//

func getTracer(err error) (tracer, bool) {

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

	t := tracer(nil)
	if !errors.As(err, &t) || t == nil {
		return nil, false
	}

	for {

		//
		// Intentionally not traversing `Unwrap() []errors` implementations here
		// as errors.As already handles that case at the above level, but more
		// importantly the tracedError type which is what we're intending to peek
		// within here at this context only implements `Unwrap() error`.
		//

		// Peek within the error to see if the the error implementing the tracer
		// wraps around another error implementing the tracer. This way we always
		// return the innermost tracer instance.
		if v, ok := t.(interface{ Unwrap() error }); ok {
			if err := v.Unwrap(); err != nil {
				if nt := tracer(nil); errors.As(err, &nt) && nt != nil {
					t = nt
					continue
				}
			}
		}

		break
	}

	return t, true
}

type stacktraceStringer stackTrace

func (s stacktraceStringer) AppendText(p []byte) ([]byte, error) {
	if len(s) != 0 {
		p = slices.Grow(p, traceSize(s))
		p = appendTrace(p, s)
	}

	return p, nil
}

func (s stacktraceStringer) marshalText() []byte {
	if len(s) == 0 {
		return nil
	}

	buf := make([]byte, 0, traceSize(s))
	return appendTrace(buf, s)
}

func (s stacktraceStringer) MarshalText() ([]byte, error) {
	return s.marshalText(), nil
}

func (s stacktraceStringer) String() string {
	return string(s.marshalText())
}

type traceStringer interface {
	String() string
	AppendText([]byte) ([]byte, error)
}

var _ traceStringer = stacktraceStringer(stackTrace(nil)) // TODO: move to tests

func Stacktrace(err error) stacktraceStringer {

	t := xi_errors.StacktraceFromError(err)
	if t == nil {
		return nil
	}

	trace := t.Stacktrace()
	if len(trace) == 0 {
		// Protects against the case where the trace is non-nil but is empty
		//
		// It should not have a stringer in that case.
		return nil
	}

	return stacktraceStringer(trace)
}
