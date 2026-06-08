package xerrors

import (
	"testing"
)

func BenchmarkWriteStacktrace(b *testing.B) {
	f := func() {
		err := New("hello there")
		if v, ok := err.(StacktraceLease); ok {
			v.ReleaseStacktrace()
		}
		_ = err
	}
	const wrapperCount = 512 + 1
	for range wrapperCount {
		vf := f
		f = func() { vf() }
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f()
	}
	b.StopTimer()
}

func Test_traceStringer(t *testing.T) {
	var pcs []uintptr

	trace := stackTrace(pcs)
	if trace != nil {
		t.Fatal("expected trace to be nil since pcs slice was nil, but it was not")
	}

	stringer := stacktraceStringer(trace)
	if stringer != nil {
		t.Fatal("expected stringer to be nil since trace was nil, but it was not")
	}
}
