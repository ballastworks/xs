package xhttp

import (
	"net/http"

	"github.com/ballastworks/xs/xmw"
)

type mwHandlerFunc = func(http.ResponseWriter, *http.Request)

// Middleware is a link in a Middleware chain that can resolve into a full handler
type Middleware = func(next http.Handler) http.Handler

type MiddlewareChain = xmw.Chain[http.Handler]
type MiddlewareChainExt MiddlewareChain

func NewMiddlewareChainHandlerFunc(links ...(func(next http.HandlerFunc) http.HandlerFunc)) MiddlewareChain {
	c := make([]Middleware, len(links))

	for i, f := range links {
		c[i] = func(next http.Handler) http.Handler {
			h := http.HandlerFunc(next.ServeHTTP)
			h = f(h)
			return h
		}
	}

	return MiddlewareChain(c)
}

func NewMiddlewareChainFunc(links ...(func(next mwHandlerFunc) mwHandlerFunc)) MiddlewareChain {
	c := make([]Middleware, len(links))

	for i, f := range links {
		c[i] = func(next http.Handler) http.Handler {
			h := next.ServeHTTP
			h = f(h)
			return http.HandlerFunc(h)
		}
	}

	return MiddlewareChain(c)
}

func (c MiddlewareChainExt) Handler(h http.Handler) http.Handler {
	if h == nil {
		panic("nil http.Handler")
	}

	return xmw.Chain[http.Handler](c).Wrap(h)
}

func (c MiddlewareChainExt) HandlerFunc(h mwHandlerFunc) http.Handler {
	if h == nil {
		panic("nil handler func")
	}

	return xmw.Chain[http.Handler](c).Wrap(http.HandlerFunc(h))
}

func NewMiddlewareChain(links ...Middleware) MiddlewareChain {
	return MiddlewareChain(xmw.NewChain(links...))
}

// UnsafeSliceToMiddlewareChain must only be used in a context where the
// lineage of the middlewares is understood and the slice context
// has no other references to it or is purely ephemeral.
//
// Use NewMiddlewareChain instead unless you understand the above.
func UnsafeSliceToMiddlewareChain(links ...Middleware) MiddlewareChain {
	return MiddlewareChain(links)
}

func NewMiddleware(links ...Middleware) Middleware {
	return MiddlewareChainExt(xmw.NewChain(links...)).Handler
}
