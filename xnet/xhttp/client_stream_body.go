package xhttp

import (
	"context"
	"io"
)

// streamBody is the response body handed to callers of DoStandard when the
// request runner does not buffer it. The request context governs body reads
// as well as the round trip, so the attempt's cancel func moves here instead
// of running when the attempt returns: the per-call deadline keeps bounding
// the body and Close releases the context once the caller is done with it.
type streamBody struct {
	io.ReadCloser
	cancel context.CancelFunc
}

// Close closes the underlying body before canceling the request context so
// a fully read connection can still return to the transport's pool.
func (b *streamBody) Close() error {
	err := b.ReadCloser.Close()
	b.cancel()
	return err
}
