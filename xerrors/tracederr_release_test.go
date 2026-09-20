package xerrors

import (
	"errors"
	"runtime/debug"
	"slices"
	"sync"
	"testing"
)

// tracedError is what getTracer finds by errors.As, so it must satisfy the
// tracer interface itself now that the pooled buffer no longer does.
var _ tracer = (*tracedError)(nil)

// TestReleaseStacktrace_SecondReleaseCannotStealALiveTrace pins the
// ownership rule of the pooled trace buffers: releasing one error's trace a
// second time must be a no-op, whatever the pool has done with the buffer in
// between.
//
// The sequence is the one every xhttp error response goes through, where the
// router's deferred release runs after Response.WriteResp has already
// released the same error: a is released once, so its buffer sits in the
// pool; b is created and takes that buffer; a is released again. The buffer
// belongs to b now. If the second release puts it back, c takes it and
// overwrites b's frames, so two live errors share one buffer and b reports
// c's stack.
func TestReleaseStacktrace_SecondReleaseCannotStealALiveTrace(t *testing.T) {
	// A GC between the steps would drain the pool and hide the reuse, and
	// the scenario is repeated because sync.Pool only hands the same buffer
	// back on the same P.
	defer debug.SetGCPercent(debug.SetGCPercent(-1))

	for i := range 64 {
		a := New("a")
		ra := StacktraceReleaser(a)
		if ra == nil {
			t.Fatal("a has no releasable trace")
		}
		ra.ReleaseStacktrace()

		b := New("b")
		bFrames := slices.Clone([]uintptr(Stacktrace(b)))
		if len(bFrames) == 0 {
			t.Fatal("b has no trace")
		}

		ra.ReleaseStacktrace() // the second release of a: must not touch b's buffer

		c := New("c")

		bNow, cNow := []uintptr(Stacktrace(b)), []uintptr(Stacktrace(c))
		if len(bNow) != 0 && len(cNow) != 0 && &bNow[0] == &cNow[0] {
			t.Fatalf("iteration %d: b and c share one trace buffer: the second release of a handed b's buffer back to the pool", i)
		}
		if !slices.Equal(bNow, bFrames) {
			t.Fatalf("iteration %d: b's frames changed after c was created", i)
		}

		StacktraceReleaser(b).ReleaseStacktrace()
		StacktraceReleaser(c).ReleaseStacktrace()
	}
}

// TestReleaseStacktrace_DoubleReleaseUnderConcurrency is the same rule under
// the race detector: double releases on one goroutine while others take
// buffers from the same pool must not touch a buffer another goroutine owns.
func TestReleaseStacktrace_DoubleReleaseUnderConcurrency(t *testing.T) {
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 2000 {
				err := New("x")
				r := StacktraceReleaser(err)
				r.ReleaseStacktrace()
				r.ReleaseStacktrace()
			}
		})
	}
	wg.Wait()
}

// TestWithStack_KeepsALiveTraceAndReplacesAReleasedOne pins the rule that
// lets a shared error (a cached result, a sentinel declared with New) log a
// correct stack on every return: WithStack leaves a chain with a live stack
// alone, and wraps anew when the chain's stack has been released.
func TestWithStack_KeepsALiveTraceAndReplacesAReleasedOne(t *testing.T) {
	shared := New("shared")
	if len(Stacktrace(shared)) == 0 {
		t.Fatal("shared has no trace")
	}

	if again := WithStack(shared); again != shared {
		t.Fatal("WithStack wrapped an error whose stack is live")
	}

	StacktraceReleaser(shared).ReleaseStacktrace()
	if len(Stacktrace(shared)) != 0 {
		t.Fatal("released error still reports frames")
	}

	again := WithStack(shared)
	if again == shared {
		t.Fatal("WithStack returned the released error unchanged: its next return would log without a trace")
	}
	if len(Stacktrace(again)) == 0 {
		t.Fatal("re-wrapped error has no trace")
	}
	if !errors.Is(again, shared) {
		t.Fatal("re-wrapped error lost its identity")
	}
	if again.Error() != shared.Error() {
		t.Fatalf("re-wrapped error changed its message: %q", again.Error())
	}

	// And the re-wrap releases like any other, leaving the shared error
	// untouched for its next return.
	StacktraceReleaser(again).ReleaseStacktrace()
	if third := WithStack(shared); third == shared || len(Stacktrace(third)) == 0 {
		t.Fatal("third return of the shared error did not get a fresh trace")
	}
}
