package xhttp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/josephcopenhaver/tbdd-go"
	"github.com/stretchr/testify/assert"
)

// newSetup records responses through an httptest.ResponseRecorder, which
// buffers them whole, so handlers here that flush headers and then stall use
// a bare httptest server instead.
func newStreamServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return srv
}

// The default transport is shared process-wide and other parallel tests close
// its idle connections during cleanup, so each client gets its own.
func newStreamClient(t *testing.T, srv *httptest.Server, perCallTimeout time.Duration, opts ...ClientOption) *Client {
	t.Helper()

	op := ClientOpts()
	c, err := NewClient(append([]ClientOption{
		op.BaseURL(srv.URL),
		op.PerCallTimeout(perCallTimeout),
		op.Transport(http.DefaultTransport.(*http.Transport).Clone()),
	}, opts...)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.CloseIdleConnections)

	return c
}

func flushHeaders(w http.ResponseWriter) {
	w.WriteHeader(http.StatusOK)
	w.(http.Flusher).Flush()
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type contextCapture struct {
	mu   sync.Mutex
	ctxs []context.Context
}

func (cc *contextCapture) middleware(next http.RoundTripper) http.RoundTripper {
	return roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		cc.mu.Lock()
		cc.ctxs = append(cc.ctxs, req.Context())
		cc.mu.Unlock()
		return next.RoundTrip(req)
	})
}

func (cc *contextCapture) last(t *testing.T) context.Context {
	t.Helper()

	cc.mu.Lock()
	defer cc.mu.Unlock()
	if len(cc.ctxs) == 0 {
		t.Fatal("no request reached the transport")
	}
	return cc.ctxs[len(cc.ctxs)-1]
}

type closeTracker struct {
	io.ReadCloser
	closed atomic.Bool
}

func (ct *closeTracker) Close() error {
	ct.closed.Store(true)
	return ct.ReadCloser.Close()
}

type bodyTracker struct {
	mu     sync.Mutex
	bodies []*closeTracker
}

func (bt *bodyTracker) middleware(next http.RoundTripper) http.RoundTripper {
	return roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		resp, err := next.RoundTrip(req)
		if resp != nil && resp.Body != nil {
			ct := &closeTracker{ReadCloser: resp.Body}
			resp.Body = ct
			bt.mu.Lock()
			bt.bodies = append(bt.bodies, ct)
			bt.mu.Unlock()
		}
		return resp, err
	})
}

func (bt *bodyTracker) closed() []bool {
	bt.mu.Lock()
	defer bt.mu.Unlock()

	closed := make([]bool, len(bt.bodies))
	for i, b := range bt.bodies {
		closed[i] = b.closed.Load()
	}
	return closed
}

func TestDoStandard_BodyReadableAfterReturn(t *testing.T) {
	t.Parallel()

	type TC struct {
		payload string
		c       *Client
	}

	type R struct {
		body    string
		readErr error
	}

	tbdd.GWT(
		TC{payload: "arrived after the headers"},
		// given
		"a server that writes the body only after DoStandard has had time to return",
		func(t *testing.T, tc *TC) {
			payload := tc.payload
			srv := newStreamServer(t, func(w http.ResponseWriter, r *http.Request) {
				flushHeaders(w)
				time.Sleep(50 * time.Millisecond)
				_, _ = io.WriteString(w, payload)
			})
			tc.c = newStreamClient(t, srv, 5*time.Second)
		},
		// when
		"the body is read after DoStandard returns",
		func(t *testing.T, tc TC) R {
			resp, err := tc.c.DoStandard(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()

			b, err := io.ReadAll(resp.Body)
			return R{body: string(b), readErr: err}
		},
		// then
		"the whole body arrives",
		func(t *testing.T, tc TC, r R) {
			is := assert.New(t)

			is.NoError(r.readErr)
			is.Equal(tc.payload, r.body)
		},
	).Run(t)
}

func TestDoStandard_BodyReadBoundedByPerCallTimeout(t *testing.T) {
	t.Parallel()

	type TC struct {
		timeout     time.Duration
		c           *Client
		handlerDone chan struct{}
	}

	type R struct {
		readErr             error
		elapsed             time.Duration
		closeErr            error
		handlerSawClientEnd bool
	}

	tbdd.GWT(
		TC{timeout: 200 * time.Millisecond, handlerDone: make(chan struct{})},
		// given
		"a server that flushes the headers then stalls until the client gives up",
		func(t *testing.T, tc *TC) {
			handlerDone := tc.handlerDone
			srv := newStreamServer(t, func(w http.ResponseWriter, r *http.Request) {
				defer close(handlerDone)
				flushHeaders(w)
				<-r.Context().Done()
			})
			tc.c = newStreamClient(t, srv, tc.timeout)
		},
		// when
		"the body is read",
		func(t *testing.T, tc TC) R {
			start := time.Now()
			resp, err := tc.c.DoStandard(context.Background())
			if err != nil {
				t.Fatal(err)
			}

			_, readErr := io.ReadAll(resp.Body)
			elapsed := time.Since(start)
			closeErr := resp.Body.Close()

			var handlerSawClientEnd bool
			select {
			case <-tc.handlerDone:
				handlerSawClientEnd = true
			case <-time.After(5 * time.Second):
			}

			return R{
				readErr:             readErr,
				elapsed:             elapsed,
				closeErr:            closeErr,
				handlerSawClientEnd: handlerSawClientEnd,
			}
		},
		// then
		"the read fails with a deadline error once the per-call timeout passes and the body still closes",
		func(t *testing.T, tc TC, r R) {
			is := assert.New(t)

			is.ErrorIs(r.readErr, context.DeadlineExceeded)
			is.GreaterOrEqual(r.elapsed, tc.timeout)
			is.Less(r.elapsed, 10*tc.timeout)
			is.NoError(r.closeErr)
			is.True(r.handlerSawClientEnd)
		},
	).Run(t)
}

func TestDoStandard_CloseReleasesAttemptContext(t *testing.T) {
	t.Parallel()

	type TC struct {
		c  *Client
		cc *contextCapture
	}

	type R struct {
		hasDeadline    bool
		errAfterReturn error
		errAfterRead   error
		errAfterClose  error
		secondCloseErr error
	}

	tbdd.GWT(
		TC{cc: &contextCapture{}},
		// given
		"a client whose transport records the attempt context",
		func(t *testing.T, tc *TC) {
			srv := newStreamServer(t, func(w http.ResponseWriter, r *http.Request) {
				flushHeaders(w)
				_, _ = io.WriteString(w, "ok")
			})
			tc.c = newStreamClient(t, srv, 5*time.Second, ClientOpts().Middlewares([]RoundTripMiddleware{tc.cc.middleware}))
		},
		// when
		"the body is read, closed, and closed again",
		func(t *testing.T, tc TC) R {
			resp, err := tc.c.DoStandard(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			ctx := tc.cc.last(t)

			var r R
			_, r.hasDeadline = ctx.Deadline()
			r.errAfterReturn = ctx.Err()

			if _, err := io.ReadAll(resp.Body); err != nil {
				t.Fatal(err)
			}
			r.errAfterRead = ctx.Err()

			if err := resp.Body.Close(); err != nil {
				t.Fatal(err)
			}
			r.errAfterClose = ctx.Err()
			r.secondCloseErr = resp.Body.Close()

			return r
		},
		// then
		"the context carries the per-call deadline, outlives the read, and is canceled only by Close",
		func(t *testing.T, tc TC, r R) {
			is := assert.New(t)

			is.True(r.hasDeadline)
			is.NoError(r.errAfterReturn)
			is.NoError(r.errAfterRead)
			is.ErrorIs(r.errAfterClose, context.Canceled)
			is.NoError(r.secondCloseErr)
		},
	).Run(t)
}

func TestDoStandard_ConnectionReusedAfterClose(t *testing.T) {
	t.Parallel()

	type TC struct {
		c *Client
	}

	type R struct {
		reused []bool
	}

	tbdd.GWT(
		TC{},
		// given
		"a server that writes a short body",
		func(t *testing.T, tc *TC) {
			srv := newStreamServer(t, func(w http.ResponseWriter, r *http.Request) {
				flushHeaders(w)
				_, _ = io.WriteString(w, "ok")
			})
			tc.c = newStreamClient(t, srv, 5*time.Second)
		},
		// when
		"two requests are made back to back, each body fully read then closed",
		func(t *testing.T, tc TC) R {
			var reused atomic.Bool
			ctx := httptrace.WithClientTrace(context.Background(), &httptrace.ClientTrace{
				GotConn: func(info httptrace.GotConnInfo) {
					reused.Store(info.Reused)
				},
			})

			var r R
			for range 2 {
				resp, err := tc.c.DoStandard(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := io.ReadAll(resp.Body); err != nil {
					t.Fatal(err)
				}
				if err := resp.Body.Close(); err != nil {
					t.Fatal(err)
				}
				r.reused = append(r.reused, reused.Load())
			}

			return r
		},
		// then
		"the second request reuses the first request's connection",
		func(t *testing.T, tc TC, r R) {
			is := assert.New(t)

			is.Equal([]bool{false, true}, r.reused)
		},
	).Run(t)
}

func TestDoStandard_CallerContextStillCancelsBody(t *testing.T) {
	t.Parallel()

	type TC struct {
		c *Client
	}

	type R struct {
		readErr      error
		readReturned bool
	}

	tbdd.GWT(
		TC{},
		// given
		"a server that flushes the headers then stalls, and a client with a long per-call timeout",
		func(t *testing.T, tc *TC) {
			srv := newStreamServer(t, func(w http.ResponseWriter, r *http.Request) {
				flushHeaders(w)
				<-r.Context().Done()
			})
			tc.c = newStreamClient(t, srv, time.Minute)
		},
		// when
		"the caller cancels its context while a body read is pending",
		func(t *testing.T, tc TC) R {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			resp, err := tc.c.DoStandard(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()

			readErr := make(chan error, 1)
			go func() {
				_, err := io.ReadAll(resp.Body)
				readErr <- err
			}()

			cancel()

			select {
			case err := <-readErr:
				return R{readErr: err, readReturned: true}
			case <-time.After(5 * time.Second):
				return R{}
			}
		},
		// then
		"the read fails with the caller's cancellation",
		func(t *testing.T, tc TC, r R) {
			is := assert.New(t)

			is.True(r.readReturned)
			is.ErrorIs(r.readErr, context.Canceled)
		},
	).Run(t)
}

func TestDoStandard_LaterErrorResponseClosedWhenFirstIsReturned(t *testing.T) {
	t.Parallel()

	type TC struct {
		c     *Client
		bt    *bodyTracker
		calls *atomic.Int32
	}

	type R struct {
		err          error
		respNil      bool
		status       int
		calls        int32
		bodiesClosed []bool
	}

	tbdd.GWT(
		TC{bt: &bodyTracker{}, calls: &atomic.Int32{}},
		// given
		"a server that answers a retryable 503 then a non-retryable 400, behind a transport that tracks body closes",
		func(t *testing.T, tc *TC) {
			calls := tc.calls
			srv := newStreamServer(t, func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 {
					w.WriteHeader(http.StatusServiceUnavailable)
					_, _ = io.WriteString(w, "first")
					return
				}
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, "second")
			})
			tc.c = newStreamClient(t, srv, 5*time.Second, ClientOpts().Middlewares([]RoundTripMiddleware{tc.bt.middleware}))
		},
		// when
		"the request is executed",
		func(t *testing.T, tc TC) R {
			resp, err := tc.c.DoStandard(context.Background())

			r := R{err: err, respNil: resp == nil, calls: tc.calls.Load(), bodiesClosed: tc.bt.closed()}
			if resp != nil {
				r.status = resp.StatusCode
			}
			return r
		},
		// then
		"the first error response is returned and both response bodies were closed",
		func(t *testing.T, tc TC, r R) {
			is := assert.New(t)

			is.Error(r.err)
			is.False(r.respNil)
			is.Equal(http.StatusServiceUnavailable, r.status)
			is.Equal(int32(2), r.calls)
			is.Equal([]bool{true, true}, r.bodiesClosed)
		},
	).Run(t)
}

func TestDoStandard_BodilessResponseReleasesContextOnReturn(t *testing.T) {
	t.Parallel()

	type TC struct {
		method string
		status int
		http2  bool
		c      *Client
		cc     *contextCapture
	}

	type R struct {
		protoMajor int
		wrapped    bool
		ctxErr     error
		body       string
		readErr    error
		closeErr   error
	}

	givenF := func(t *testing.T, tc *TC) {
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
		})

		var srv *httptest.Server
		var transport http.RoundTripper
		if tc.http2 {
			srv = httptest.NewUnstartedServer(handler)
			srv.EnableHTTP2 = true
			srv.StartTLS()
			t.Cleanup(srv.Close)
			transport = srv.Client().Transport
		} else {
			srv = newStreamServer(t, handler)
			transport = http.DefaultTransport.(*http.Transport).Clone()
		}

		op := ClientOpts()
		tc.cc = &contextCapture{}
		tc.c = newStreamClient(t, srv, 5*time.Second,
			op.Transport(transport),
			op.Middlewares([]RoundTripMiddleware{tc.cc.middleware}),
			op.Method(tc.method),
		)
	}

	whenF := func(t *testing.T, tc TC) R {
		resp, err := tc.c.DoStandard(context.Background())
		if err != nil {
			t.Fatal(err)
		}

		var r R
		r.protoMajor = resp.ProtoMajor
		_, r.wrapped = resp.Body.(*streamBody)
		r.ctxErr = tc.cc.last(t).Err()

		b, err := io.ReadAll(resp.Body)
		r.body, r.readErr = string(b), err
		r.closeErr = resp.Body.Close()

		return r
	}

	thenF := func(t *testing.T, tc TC, r R) {
		is := assert.New(t)

		if tc.http2 {
			is.Equal(2, r.protoMajor)
		}
		is.False(r.wrapped)
		is.ErrorIs(r.ctxErr, context.Canceled)
		is.NoError(r.readErr)
		is.Empty(r.body)
		is.NoError(r.closeErr)
	}

	for _, tc := range []struct {
		name string
		tc   TC
	}{
		{"http1 204", TC{method: http.MethodGet, status: http.StatusNoContent}},
		{"http1 content-length 0", TC{method: http.MethodGet, status: http.StatusOK}},
		{"http1 head", TC{method: http.MethodHead, status: http.StatusOK}},
		{"http2 204", TC{method: http.MethodGet, status: http.StatusNoContent, http2: true}},
		{"http2 head", TC{method: http.MethodHead, status: http.StatusOK, http2: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tbdd.GWT(
				tc.tc,
				// given
				"a server whose response has no body",
				givenF,
				// when
				"the request is executed and the empty body is read and closed",
				whenF,
				// then
				"the body is handed over unwrapped with its attempt context already released",
				thenF,
			).Run(t)
		})
	}
}
