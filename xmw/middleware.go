package xmw

type Chain[T any] [](func(next T) T)

func NewChain[T any](links ...(func(next T) T)) Chain[T] {

	if len(links) == 0 {
		return nil
	}

	c := make([](func(next T) T), len(links))
	copy(c, links)

	return c
}

// UnsafeSliceToChain must only be used in a context where the
// lineage of the middlewares is understood and the slice context
// has no other references to it or is purely ephemeral.
//
// Use NewChain instead unless you understand the above.
func UnsafeSliceToChain[T any](links ...func(next T) T) Chain[T] {
	return Chain[T](links)
}

func (c Chain[T]) appendChains(chains ...Chain[T]) Chain[T] {

	totalSize := len(c)
	var delta int

	for _, links := range chains {
		delta += len(links)
		if delta < len(links) {
			panic("integer overflow")
		}
	}

	if delta == 0 {
		if totalSize == 0 {
			return nil
		}
		return c
	}

	totalSize += delta
	if totalSize < delta {
		panic("integer overflow")
	}

	result := make([](func(next T) T), totalSize)

	copy(result, c)
	dst := result[len(c):]

	for _, links := range chains {
		copy(dst, links)
		dst = dst[len(links):]
	}

	return result
}

func (c Chain[T]) Append(links ...(func(next T) T)) Chain[T] {

	if len(links) == 0 {
		return c
	}

	return c.appendChains(links)
}

func (c Chain[T]) AppendChains(chains ...Chain[T]) Chain[T] {

	if len(chains) == 0 {
		return c
	}

	return c.appendChains(chains...)
}

// Wrap does not check the validity of the passed in argument v.
//
// If making a wrapping middleware type ensure to validate
// accordingly before calling this method.
func (c Chain[T]) Wrap(v T) T {

	for i := len(c) - 1; i >= 0; i-- {
		v = c[i](v)
	}

	return v
}

// The function returned by New does not validate the argument
// it receives.
//
// If making a wrapping middleware type ensure to validate
// accordingly before calling this method.
func New[T any](links ...(func(T) T)) func(T) T {
	return NewChain(links...).Wrap
}
