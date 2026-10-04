package xhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestClientRetryErrorReturned ensures the reason retrying stopped is part of
// the error Do returns, so callers can match its exported sentinel alongside
// the error of the response being returned.
func TestClientRetryErrorReturned(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	c, err := NewClient(ClientOpts().BaseURL(srv.URL))
	require.NoError(t, err)

	_, err = c.Do(context.Background(),
		ReqOpts().Retry(true),
		ReqOpts().RetryMaxTimeout(20*time.Millisecond),
	)

	require.ErrorIs(t, err, ErrRespStatusCodeNotSuccess)
	require.ErrorIs(t, err, ErrRetryDeadlineExceeded)
}
