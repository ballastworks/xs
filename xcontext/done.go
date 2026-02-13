package xcontext

import (
	"context"
)

// Done returns true if a context has been canceled
//
// If you need a cause or reason for a context being
// done, then please use either xcontext.Cause(<context.Context>) or ctx.Err().
//
// If this call is too hot because the context has many direct
// child contexts all interested in short-circuiting when the parent
// context is done, then consider using xcontext.ManyChildrenOptimizedDone
// instead.
func Done(ctx context.Context) bool {
	return (ctx.Err() != nil)
}

// ManyChildrenOptimizedDone is functionally the same as xcontext.Done
// but optimized for cases where a context has many, many direct child contexts
// all interested in short-circuiting when the parent context is done.
//
// It can lead to additional allocations of the context's done channel
// if it has not already been allocated, but avoids lock contention
// and context switches that can occur when many goroutines
// are all trying to check the done state of a single parent context
// via ctx.Err() or xcontext.Done().
//
// In simple cases it is not wall-clock faster than xcontext.Done().
//
// Use only if performance testing shows it to be beneficial to your use case.
func ManyChildrenOptimizedDone(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return true
	default:
		return false
	}
}

// Note that I strongly believe no ManyChildrenOptimizedErr() function is needed
// because the Err() result is not a cause by its nature. We'd be optimizing for
// returning an issue indicator that will likely communicate context.Canceled
// or context.DeadlineExceeded instead of a true cause error. Such an assumption
// can already be made via the ManyChildrenOptimizedDone function as it is just
// as imprecise and communicates the same informational intent in a many-children
// optimized manner. This combined with the fact that context.Context.Err() exists
// for the not-many-children-optimized case means that no additional behavior is
// needed here unless I just wanted to soothe some OCD tendencies to make the API
// surface more symmetrical. Hard pass.

// Cause is different from context.Cause() because it guarantees that it returns
// the cause error when the result is non-nil. Standard context.Cause as of
// go1.22 falls back to returning the .Err() result and does so without the
// protection of a lock after checking for cause existence. This means that the
// .Err() call can return a non-nil value that is not the cause if the context
// expires between the lock's protection and the call to .Err().
//
// It's safe to just use the standard `context.Cause(<context.Context>) error` function
// if it is known that the context has already reached a steady "done" state.
func Cause(ctx context.Context) error {
	if ctx.Err() != nil {
		return context.Cause(ctx)
	}

	return nil
}

// ManyChildrenOptimizedCause is functionally the same as xcontext.Cause
// but optimized for cases where a context has many, many direct child contexts
// all interested in short-circuiting when the parent context is done.
//
// It can lead to additional allocations of the context's done channel
// if it has not already been allocated, but avoids lock contention
// and context switches that can occur when many goroutines
// are all trying to check the done state of a single parent context
// via ctx.Err() or xcontext.Done().
//
// In simple cases it is not wall-clock faster than xcontext.Cause().
//
// Use only if performance testing shows it to be beneficial to your use case.
func ManyChildrenOptimizedCause(ctx context.Context) error {
	if ManyChildrenOptimizedDone(ctx) {
		return context.Cause(ctx)
	}

	return nil
}
