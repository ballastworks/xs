package xmw

import (
	"testing"
)

func BenchmarkAppend(b *testing.B) {
	chain := NewChain(func(next int) int { return next + 1 })

	links := []func(int) int{
		func(next int) int { return next * 2 },
		func(next int) int { return next - 3 },
		func(next int) int { return next + 5 },
	}

	for i := 0; i < b.N; i++ {
		_ = chain.Append(links...)
	}
}
