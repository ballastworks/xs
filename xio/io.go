package xio

import (
	"io"

	"github.com/ballastworks/xs/internal/xi_io"
)

// this entire file is just copied from go1.21's SDK so functional testing can look at internal types

// NopCloser returns a ReadCloser with a no-op Close method wrapping
// the provided Reader r.
// If r implements WriterTo, the returned ReadCloser will implement WriterTo
// by forwarding calls to r.
func NopCloser(r io.Reader) io.ReadCloser {
	if v, ok := r.(xi_io.WriterToReader); ok {
		w := xi_io.NopCloserWriterToReaderPool.Get().(*xi_io.NopCloserWriterToReader)
		w.WriterToReader = v
		return w
	}

	w := xi_io.NopCloserPool.Get().(*xi_io.NopCloser)
	w.Reader = r
	return w
}
