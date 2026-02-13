package xhttp

import (
	"sync/atomic"
	"unsafe"
)

func checkNewDefaultResponseFactoryAtomic(err error) {
	if err != nil {
		panic(err)
	}
}

func newDefaultRespFactoryAtomic() unsafe.Pointer {
	respf, err := NewResponseFactory()
	checkNewDefaultResponseFactoryAtomic(err)

	return unsafe.Pointer(respf)
}

var defaultRespFactoryAtomic = newDefaultRespFactoryAtomic()

func DefaultResponseFactory() *ResponseFactory {
	return (*ResponseFactory)(atomic.LoadPointer(&defaultRespFactoryAtomic))
}

// SetDefaultResponseFactory replaces the old default response factory and returns the old instance
func SetDefaultResponseFactory(factory *ResponseFactory) *ResponseFactory {
	if factory == nil {
		panic("nil response factory")
	}

	return (*ResponseFactory)(atomic.SwapPointer(&defaultRespFactoryAtomic, unsafe.Pointer(factory)))
}
