package xhttp

import (
	"net/http"
	"testing"
)

func BenchmarkMiddlewareAppendChain(b *testing.B) {
	mw := NewMiddlewareChain(func(next http.Handler) http.Handler { return next })
	for range b.N {
		c := UnsafeSliceToMiddlewareChain(
			func(next http.Handler) http.Handler { return next },
		)
		_ = mw.AppendChains(c)
	}
}
