package xhttp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/josephcopenhaver/tbdd-go"
	"github.com/stretchr/testify/assert"
)

func bodyRequiresClose(v any) bool {
	if v == nil {
		return false
	}

	var r io.Reader
	switch vr := v.(type) {
	case *ClientResponse:
		if vr == nil || vr.Body == nil {
			return false
		}
		r = vr.Body
	case *http.Response:
		if vr == nil || vr.Body == nil {
			return false
		}
		r = vr.Body
	}

	if r == nil {
		panic("unexpected input type to bodyRequiresClose(*ClientResponse | *http.Response)")
	}

	if v, ok := r.(interface{ IsNopCloser() bool }); ok && v != nil && v.IsNopCloser() {
		return false
	}

	return true
}

type roundTrip struct {
	req                    *http.Request
	resp                   *http.Response
	handleStart, handleEnd time.Time
}

type testSetup struct {
	rwm        sync.RWMutex
	h          http.Handler
	srv        *httptest.Server
	c          *Client
	roundTrips []roundTrip
}

func (ts *testSetup) Close() {
	if srv := ts.srv; srv != nil {
		defer func() {
			ts.srv = nil
			srv.Close()
		}()
	}

	if c := ts.c; c != nil {
		defer func() {
			ts.c = nil
			c.CloseIdleConnections()
		}()
	}
}

func (ts *testSetup) SetHandlerFunc(f http.HandlerFunc) {
	ts.SetHandler(f)
}

func (ts *testSetup) SetHandler(h http.Handler) {
	ts.rwm.Lock()
	defer ts.rwm.Unlock()
	ts.h = ts.middleware(h)
}

func (ts *testSetup) BaseURL() string {
	return ts.srv.URL
}

func (ts *testSetup) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := func() http.Handler {
		ts.rwm.RLock()
		defer ts.rwm.RUnlock()
		return ts.h
	}()

	if h != nil {
		h.ServeHTTP(w, r)
	}
}

func (ts *testSetup) Client() *Client {
	return ts.c
}

func (ts *testSetup) nextRoundTrip() *roundTrip {
	ts.rwm.Lock()
	defer ts.rwm.Unlock()

	i := len(ts.roundTrips)
	ts.roundTrips = append(ts.roundTrips, roundTrip{})
	return &ts.roundTrips[i]
}

func (ts *testSetup) PeekRoundTrips() []roundTrip {
	ts.rwm.RLock()
	defer ts.rwm.RUnlock()

	rts := slices.Clone(ts.roundTrips)
	for i := range rts {
		if req := rts[i].req; req != nil {
			if v, ok := req.Body.(erroredNopReader); ok {
				req.Body = erroredNopReader{v.err, bytes.NewBuffer(append([]byte(nil), v.buf.Bytes()...))}
			}
		}
		if resp := rts[i].resp; resp != nil {
			if v, ok := resp.Body.(erroredNopReader); ok {
				resp.Body = erroredNopReader{v.err, bytes.NewBuffer(append([]byte(nil), v.buf.Bytes()...))}
			}
		}
	}

	return rts
}

func (ts *testSetup) PopRoundTrips() []roundTrip {
	ts.rwm.Lock()
	defer ts.rwm.Unlock()

	rts := ts.roundTrips
	ts.roundTrips = nil

	return rts
}

func (ts *testSetup) NumRoundTrips() int {
	ts.rwm.RLock()
	defer ts.rwm.RUnlock()

	return len(ts.roundTrips)
}

func (ts *testSetup) middleware(h http.Handler) http.Handler {
	middlewares := [](func(http.Handler) http.Handler){
		func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// track round trip middleware
				handleStart := time.Now()

				rr := httptest.NewRecorder()
				rtp := ts.nextRoundTrip()
				rtp.handleStart = handleStart

				body := r.Body
				r.Body = nil
				rtp.req = r.Clone(context.Background())

				var buf bytes.Buffer
				_, err := io.Copy(&buf, body)
				if err != nil {
					r.Body = erroredNopReader{err, bytes.NewBuffer(append([]byte(nil), buf.Bytes()...))}
				} else {
					r.Body = io.NopCloser(bytes.NewReader(append([]byte(nil), buf.Bytes()...)))
				}
				rtp.req.Body = erroredNopReader{nil, &buf}

				var saveResp *http.Response
				defer func() {
					if saveResp == nil {
						return
					}
					rtp.resp = saveResp

					if body := rtp.resp.Body; body != nil {
						defer func() {
							if r := recover(); r != nil {
								defer panic(r)
								rtp.resp.Body = erroredNopReader{errors.New("panicking in round trip observer"), bytes.NewBuffer([]byte(nil))}
							}
						}()
						var buf bytes.Buffer
						_, err := io.Copy(&buf, body)
						if err != nil {
							panic(err)
						}
						rtp.resp.Body = erroredNopReader{nil, &buf}
					}

					rtp.handleEnd = time.Now()
				}()

				next.ServeHTTP(rr, r)
				renderResp := rr.Result()
				saveResp = rr.Result()

				{
					h := w.Header()
					for k, v := range renderResp.Header.Clone() {
						h[k] = v
					}
				}
				w.WriteHeader(renderResp.StatusCode)
				if renderResp.Body != nil {
					_, ignoredErr := io.Copy(w, renderResp.Body)
					_ = ignoredErr
				}
			})
		},
	}

	for i := len(middlewares) - 1; i >= 0; i-- {
		h = middlewares[i](h)
	}

	return h
}

func defaultHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"Status":"ok"}` + "\n"))
	})
}

func newSetup() *testSetup {
	ts := &testSetup{}
	ts.h = ts.middleware(defaultHandler())

	srv := httptest.NewServer(ts)
	ts.srv = srv

	op := ClientOpts()
	c, err := NewClient(op.BaseURL(srv.URL))
	if err != nil {
		panic(err)
	}
	ts.c = c

	return ts
}

func TestDefaultClient(t *testing.T) {
	t.Parallel()

	type R struct {
		respErr           error
		body              []byte
		bodyErr           error
		header            http.Header
		statusCode        int
		numRoundTrips     int
		bodyRequiresClose bool
	}

	type TC struct {
		ts  *testSetup
		exp R
	}

	tbdd.WT(
		TC{
			ts: newSetup(),
			exp: R{
				body:              []byte(`{"Status":"ok"}` + "\n"),
				bodyRequiresClose: false,
				statusCode:        200,
				numRoundTrips:     1,
			},
		},
		"running a client request",
		func(t *testing.T, tc TC) R {
			ts := tc.ts
			defer ts.Close()

			resp, respErr := ts.Client().Do(context.Background())
			r := R{
				header:            resp.Header,
				respErr:           respErr,
				bodyRequiresClose: bodyRequiresClose(resp),
			}
			if respErr == nil {
				r.body, r.bodyErr = io.ReadAll(resp.Body)
			}

			r.statusCode = resp.StatusCode
			r.numRoundTrips = ts.NumRoundTrips()

			return r
		},
		"the service is called as expected",
		func(t *testing.T, tc TC, r R) {
			is := assert.New(t)

			header := r.header
			r.header = nil
			is.Equal(tc.exp, r)
			is.Equal("application/json", header.Get("Content-Type"))
		},
	).Run(t)

	tbdd.WT(
		TC{
			ts: newSetup(),
			exp: R{
				body:              []byte(`{"Status":"ok"}` + "\n"),
				bodyRequiresClose: false,
				statusCode:        200,
				numRoundTrips:     1,
			},
		},
		"running an otel client request",
		func(t *testing.T, tc TC) R {
			ts := tc.ts
			defer ts.Close()

			resp, respErr := ts.Client().Do(context.Background())
			r := R{
				header:            resp.Header,
				respErr:           respErr,
				bodyRequiresClose: bodyRequiresClose(resp),
			}
			if respErr == nil {
				r.body, r.bodyErr = io.ReadAll(resp.Body)
			}

			r.statusCode = resp.StatusCode
			r.numRoundTrips = ts.NumRoundTrips()

			return r
		},
		"the service is called as expected",
		func(t *testing.T, tc TC, r R) {
			is := assert.New(t)

			header := r.header
			r.header = nil
			is.Equal(tc.exp, r)
			is.Equal("application/json", header.Get("Content-Type"))
		},
	).Run(t)

	{
		type TC struct{}
		type R struct {
			c   any
			err error
		}

		const baseURL = "http://127.0.0.1/"
		const badBaseURL = "://rawr/"
		const badMethod = "@"

		tbdd.WT(
			TC{},
			"client has a bad base method",
			func(t *testing.T, tc TC) R {
				op := ClientOpts()
				c, err := NewClient(
					op.BaseURL(baseURL),
					op.Method(badMethod),
				)

				return R{c, err}
			},
			"new errors with ErrBadMethod",
			func(t *testing.T, tc TC, r R) {
				is := assert.New(t)

				is.Nil(r.c)
				is.NotNil(r.err)
				is.ErrorIs(r.err, ErrBadMethod)
				is.Equal(ErrBadMethod.Error()+": "+badMethod, r.err.Error())
			},
		).Run(t)

		expErr := &url.Error{
			Op:  "parse",
			URL: badBaseURL,
			Err: errors.New("missing protocol scheme"),
		}

		tbdd.WT(
			TC{},
			"client has a bad base URL",
			func(t *testing.T, tc TC) R {
				op := ClientOpts()
				c, err := NewClient(
					op.BaseURL(badBaseURL),
				)

				return R{c, err}
			},
			"new errors with url parse error",
			func(t *testing.T, tc TC, r R) {
				is := assert.New(t)

				is.Nil(r.c)
				is.NotNil(r.err)
				expTypeVal := &url.Error{}
				is.ErrorAs(r.err, &expTypeVal)
				is.Equal(expTypeVal, r.err.(*url.Error))
				is.Equal(expErr.Error(), r.err.Error())
			},
		).Run(t)
	}
}

// default client tests
// default otel client tests

// var _ = Context("new client error tests", func() {
// 	baseURL := "http://127.0.0.1/"

// 	Context("given a bad base url", func() {

// 		baseURL := "://rawr/"

// 		It("causes new to error", func() {
// 			op := ClientOpts()
// 			c, err := NewClient(
// 				op.BaseURL(baseURL),
// 			)
// 			Expect(err).ToNot(BeNil())
// 			Expect(err).To(Equal(&url.Error{
// 				Op:  "parse",
// 				URL: baseURL,
// 				Err: errors.New("missing protocol scheme"),
// 			}))
// 			Expect(c).To(BeNil())
// 		})
// 	})

// 	Context("given a negative per call timeout", func() {

// 		perCallTimeout := time.Duration(-1)

// 		It("causes new to error", func() {
// 			op := ClientOpts()
// 			c, err := NewClient(
// 				op.BaseURL(baseURL),
// 				op.PerCallTimeout(perCallTimeout),
// 			)
// 			Expect(err).ToNot(BeNil())
// 			Expect(c).To(BeNil())
// 			Expect(err).To(Equal(ErrPerCallTimeoutMustBeGTZero))
// 		})
// 	})
// })

// var _ = Context("do preflight error tests", func() {
// 	var ts *testSetup
// 	BeforeEach(func() { ts = newSetup() })
// 	AfterEach(func() { ts.Close() })

// 	It("when called with a nil unmarshal response target", func() {
// 		op := ReqOpts()
// 		resp, err := ts.Client().Do(context.Background(), op.UnmarshalJSONRespTo(nil))
// 		Expect(err).To(Equal(ErrNilUnmarshalTarget))
// 		Expect(resp).To(BeNil())
// 		Expect(bodyRequiresClose(resp)).To(BeFalse())
// 		Expect(ts.NumRoundTrips()).To(Equal(0))
// 	})

// 	It("when called with a nil GetBody", func() {
// 		op := ReqOpts()
// 		resp, err := ts.Client().Do(context.Background(), op.BodyAndGetter(http.NoBody, nil))
// 		Expect(err).To(Equal(ErrNilGetBodyNotAllowedWhenRetrying))
// 		Expect(resp).To(BeNil())
// 		Expect(bodyRequiresClose(resp)).To(BeFalse())
// 		Expect(ts.NumRoundTrips()).To(Equal(0))
// 	})

// 	It("when called with a bad method", func() {
// 		op := ReqOpts()
// 		resp, err := ts.Client().Do(context.Background(), op.Method("@"))
// 		Expect(err).ToNot(BeNil())
// 		Expect(resp).To(BeNil())
// 		Expect(errors.Is(err, ErrBadMethod)).To(BeTrue())
// 	})

// })

// var _ = Context("path", func() {
// 	var ts *testSetup
// 	BeforeEach(func() { ts = newSetup() })
// 	AfterEach(func() { ts.Close() })

// 	It("when called with a specific path 'rawr' uses the path '/rawr'", func() {
// 		op := ReqOpts()
// 		resp, err := ts.Client().Do(context.Background(), op.Path("rawr"))
// 		Expect(err).To(BeNil())
// 		Expect(resp).ToNot(BeNil())
// 		Expect(resp.StatusCode).To(Equal(200))
// 		Expect(resp.Body).ToNot(BeNil())
// 		Expect(resp.Header.Get("Content-Type")).To(Equal("application/json"))
// 		Expect(bodyRequiresClose(resp)).To(BeFalse())
// 		b, err := io.ReadAll(resp.Body)
// 		Expect(err).To(BeNil())
// 		Expect(string(b)).To(Equal(`{"Status":"ok"}` + "\n"))
// 		rts := ts.PopRoundTrips()
// 		Expect(len(rts)).To(Equal(1))
// 		Expect(rts[0].req.URL.Path).To(Equal("/rawr"))
// 	})

// 	It("when called with a specific path '/rawr' uses the path '/rawr'", func() {
// 		op := ReqOpts()
// 		resp, err := ts.Client().Do(context.Background(), op.Path("/rawr"))
// 		Expect(err).To(BeNil())
// 		Expect(resp).ToNot(BeNil())
// 		Expect(resp.StatusCode).To(Equal(200))
// 		Expect(resp.Body).ToNot(BeNil())
// 		Expect(resp.Header.Get("Content-Type")).To(Equal("application/json"))
// 		Expect(bodyRequiresClose(resp)).To(BeFalse())
// 		b, err := io.ReadAll(resp.Body)
// 		Expect(err).To(BeNil())
// 		Expect(string(b)).To(Equal(`{"Status":"ok"}` + "\n"))
// 		rts := ts.PopRoundTrips()
// 		Expect(len(rts)).To(Equal(1))
// 		Expect(rts[0].req.URL.Path).To(Equal("/rawr"))
// 	})

// 	It("when called with a specific path 'rawr/' uses the path '/rawr/'", func() {
// 		op := ReqOpts()
// 		resp, err := ts.Client().Do(context.Background(), op.Path("rawr/"))
// 		Expect(err).To(BeNil())
// 		Expect(resp).ToNot(BeNil())
// 		Expect(resp.StatusCode).To(Equal(200))
// 		Expect(resp.Body).ToNot(BeNil())
// 		Expect(resp.Header.Get("Content-Type")).To(Equal("application/json"))
// 		Expect(bodyRequiresClose(resp)).To(BeFalse())
// 		b, err := io.ReadAll(resp.Body)
// 		Expect(err).To(BeNil())
// 		Expect(string(b)).To(Equal(`{"Status":"ok"}` + "\n"))
// 		rts := ts.PopRoundTrips()
// 		Expect(len(rts)).To(Equal(1))
// 		Expect(rts[0].req.URL.Path).To(Equal("/rawr/"))
// 	})

// 	It("when called with a specific path '/rawr/' uses the path '/rawr/'", func() {
// 		op := ReqOpts()
// 		resp, err := ts.Client().Do(context.Background(), op.Path("/rawr/"))
// 		Expect(err).To(BeNil())
// 		Expect(resp).ToNot(BeNil())
// 		Expect(resp.StatusCode).To(Equal(200))
// 		Expect(resp.Body).ToNot(BeNil())
// 		Expect(resp.Header.Get("Content-Type")).To(Equal("application/json"))
// 		Expect(bodyRequiresClose(resp)).To(BeFalse())
// 		b, err := io.ReadAll(resp.Body)
// 		Expect(err).To(BeNil())
// 		Expect(string(b)).To(Equal(`{"Status":"ok"}` + "\n"))
// 		rts := ts.PopRoundTrips()
// 		Expect(len(rts)).To(Equal(1))
// 		Expect(rts[0].req.URL.Path).To(Equal("/rawr/"))
// 	})

// })

// var _ = Context("json unmarshal", func() {
// 	var ts *testSetup
// 	BeforeEach(func() { ts = newSetup() })
// 	AfterEach(func() { ts.Close() })

// 	It("when DoStandard called with no json unmarshal target inflates as expected", func() {

// 		resp, err := ts.Client().DoStandard(context.Background())
// 		Expect(err).To(BeNil())
// 		Expect(resp).ToNot(BeNil())
// 		Expect(resp.StatusCode).To(Equal(200))
// 		Expect(resp.Body).ToNot(BeNil())
// 		Expect(resp.Header.Get("Content-Type")).To(Equal("application/json"))
// 		Expect(bodyRequiresClose(resp)).To(BeTrue())
// 		defer resp.Body.Close()
// 		b, err := io.ReadAll(resp.Body)
// 		Expect(err).To(BeNil())
// 		Expect(string(b)).To(Equal(`{"Status":"ok"}` + "\n"))
// 		Expect(ts.NumRoundTrips()).To(Equal(1))
// 	})

// 	It("when DoStandard called with no json unmarshal target and server 500s for the first request, it inflates as expected", func() {

// 		ts.SetHandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 			ts.SetHandler(defaultHandler())
// 			w.WriteHeader(http.StatusInternalServerError)
// 		})

// 		resp, err := ts.Client().DoStandard(context.Background())
// 		Expect(err).To(BeNil())
// 		Expect(resp).ToNot(BeNil())
// 		Expect(resp.StatusCode).To(Equal(200))
// 		Expect(resp.Body).ToNot(BeNil())
// 		Expect(resp.Header.Get("Content-Type")).To(Equal("application/json"))
// 		Expect(bodyRequiresClose(resp)).To(BeTrue())
// 		defer resp.Body.Close()
// 		b, err := io.ReadAll(resp.Body)
// 		Expect(err).To(BeNil())
// 		Expect(string(b)).To(Equal(`{"Status":"ok"}` + "\n"))
// 		Expect(ts.NumRoundTrips()).To(Equal(2))
// 	})

// 	It("when DoStandard called with no json unmarshal target and server 500s for the first request then 429s for all others without Retry-After response headers, it inflates as expected", func() {

// 		ts.SetHandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 			ts.SetHandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 				w.Header().Set("Foo", "bar")
// 				w.WriteHeader(http.StatusTooManyRequests)
// 			})
// 			w.WriteHeader(http.StatusInternalServerError)
// 		})

// 		resp, err := ts.Client().DoStandard(context.Background())
// 		Expect(err).ToNot(BeNil())
// 		Expect(errors.Is(err, ErrRespStatusCodeNotSuccess)).To(BeTrue())
// 		Expect(err.Error()).To(HaveSuffix(": " + strconv.Itoa(http.StatusInternalServerError)))
// 		Expect(resp).ToNot(BeNil())
// 		Expect(resp.StatusCode).To(Equal(http.StatusInternalServerError))
// 		Expect(resp.Body).ToNot(BeNil())
// 		Expect(bodyRequiresClose(resp)).To(BeFalse())
// 		// callers should still defer a close though in practice as it is standard
// 		defer resp.Body.Close()
// 		b, err := io.ReadAll(resp.Body)
// 		Expect(err).To(BeNil())
// 		Expect(string(b)).To(Equal(``))
// 		Expect(ts.NumRoundTrips()).To(Equal(2))
// 		rts := ts.PopRoundTrips()
// 		Expect(rts[0].resp.Header).To(Equal(http.Header(map[string][]string{})))
// 		h := resp.Header.Clone()
// 		Expect(h).To(Equal(http.Header(map[string][]string{
// 			"Content-Length": {"0"},
// 			"Date":           {h.Get("Date")},
// 		})))
// 		Expect(rts[1].resp.Header).To(Equal(http.Header(map[string][]string{
// 			"Foo": {"bar"},
// 		})))
// 	})

// 	It("when called with a json unmarshal target (Response) and SetProtocolResponse(true) inflates as expected", func() {

// 		type respType struct {
// 			xnet.Response[ClientResponse]
// 			json.RawMessage
// 		}

// 		var respTarget respType

// 		op := ReqOpts()
// 		resp, err := ts.Client().Do(context.Background(), op.UnmarshalJSONRespTo(&respTarget), op.SetProtocolResponse(true))
// 		Expect(err).To(BeNil())
// 		Expect(resp).ToNot(BeNil())
// 		Expect(resp.StatusCode).To(Equal(200))
// 		Expect(resp.Body).ToNot(BeNil())
// 		Expect(resp.Header.Get("Content-Type")).To(Equal("application/json"))
// 		Expect(bodyRequiresClose(resp)).To(BeFalse())
// 		b, err := io.ReadAll(resp.Body)
// 		Expect(err).To(BeNil())
// 		Expect(string(b)).To(Equal(`{"Status":"ok"}` + "\n"))
// 		Expect(ts.NumRoundTrips()).To(Equal(1))
// 		Expect(respTarget.ProtocolResponse()).ToNot(BeNil())
// 		Expect(string(respTarget.RawMessage)).To(Equal(`{"Status":"ok"}`))
// 	})

// 	It("when called with a json unmarshal target (http.Response) and SetProtocolResponse(true) inflates as expected", func() {

// 		type respType struct {
// 			xnet.Response[http.Response]
// 			json.RawMessage
// 		}

// 		var respTarget respType

// 		op := ReqOpts()
// 		resp, err := ts.Client().Do(context.Background(), op.UnmarshalJSONRespTo(&respTarget), op.SetProtocolResponse(true))
// 		Expect(err).To(BeNil())
// 		Expect(resp).ToNot(BeNil())
// 		Expect(resp.StatusCode).To(Equal(200))
// 		Expect(resp.Body).ToNot(BeNil())
// 		Expect(resp.Header.Get("Content-Type")).To(Equal("application/json"))
// 		Expect(bodyRequiresClose(resp)).To(BeFalse())
// 		b, err := io.ReadAll(resp.Body)
// 		Expect(err).To(BeNil())
// 		Expect(string(b)).To(Equal(`{"Status":"ok"}` + "\n"))
// 		Expect(ts.NumRoundTrips()).To(Equal(1))
// 		Expect(respTarget.ProtocolResponse()).ToNot(BeNil())
// 		Expect(string(respTarget.RawMessage)).To(Equal(`{"Status":"ok"}`))
// 	})

// 	It("when DoStandard called with a json unmarshal target (http.Response) and SetProtocolResponse(true) inflates as expected", func() {

// 		type respType struct {
// 			xnet.Response[http.Response]
// 			json.RawMessage
// 		}

// 		var respTarget respType

// 		op := ReqOpts()
// 		resp, err := ts.Client().DoStandard(context.Background(), op.UnmarshalJSONRespTo(&respTarget), op.SetProtocolResponse(true))
// 		Expect(err).To(BeNil())
// 		Expect(resp).ToNot(BeNil())
// 		Expect(resp.StatusCode).To(Equal(200))
// 		Expect(resp.Body).ToNot(BeNil())
// 		Expect(resp.Header.Get("Content-Type")).To(Equal("application/json"))
// 		Expect(bodyRequiresClose(resp)).To(BeFalse())
// 		b, err := io.ReadAll(resp.Body)
// 		Expect(err).To(BeNil())
// 		Expect(string(b)).To(Equal(`{"Status":"ok"}` + "\n"))
// 		Expect(ts.NumRoundTrips()).To(Equal(1))
// 		Expect(respTarget.ProtocolResponse()).ToNot(BeNil())
// 		Expect(string(respTarget.RawMessage)).To(Equal(`{"Status":"ok"}`))
// 	})

// 	It("when called with a json unmarshal target (Response) and SetProtocolResponse(false) inflates as expected", func() {
// 		type respType struct {
// 			xnet.Response[ClientResponse]
// 			json.RawMessage
// 		}

// 		var respTarget respType

// 		op := ReqOpts()
// 		resp, err := ts.Client().Do(context.Background(), op.UnmarshalJSONRespTo(&respTarget), op.SetProtocolResponse(false))
// 		Expect(err).To(BeNil())
// 		Expect(resp).ToNot(BeNil())
// 		Expect(resp.StatusCode).To(Equal(200))
// 		Expect(resp.Body).ToNot(BeNil())
// 		Expect(resp.Header.Get("Content-Type")).To(Equal("application/json"))
// 		Expect(bodyRequiresClose(resp)).To(BeFalse())
// 		b, err := io.ReadAll(resp.Body)
// 		Expect(err).To(BeNil())
// 		Expect(string(b)).To(Equal(`{"Status":"ok"}` + "\n"))
// 		Expect(ts.NumRoundTrips()).To(Equal(1))
// 		Expect(respTarget.ProtocolResponse()).To(BeNil())
// 		Expect(string(respTarget.RawMessage)).To(Equal(`{"Status":"ok"}`))
// 	})

// 	It("when called with a json unmarshal target (http.Response) and SetProtocolResponse(false) inflates as expected", func() {
// 		type respType struct {
// 			xnet.Response[http.Response]
// 			json.RawMessage
// 		}

// 		var respTarget respType

// 		op := ReqOpts()
// 		resp, err := ts.Client().Do(context.Background(), op.UnmarshalJSONRespTo(&respTarget), op.SetProtocolResponse(false))
// 		Expect(err).To(BeNil())
// 		Expect(resp).ToNot(BeNil())
// 		Expect(resp.StatusCode).To(Equal(200))
// 		Expect(resp.Body).ToNot(BeNil())
// 		Expect(resp.Header.Get("Content-Type")).To(Equal("application/json"))
// 		Expect(bodyRequiresClose(resp)).To(BeFalse())
// 		b, err := io.ReadAll(resp.Body)
// 		Expect(err).To(BeNil())
// 		Expect(string(b)).To(Equal(`{"Status":"ok"}` + "\n"))
// 		Expect(ts.NumRoundTrips()).To(Equal(1))
// 		Expect(respTarget.ProtocolResponse()).To(BeNil())
// 		Expect(string(respTarget.RawMessage)).To(Equal(`{"Status":"ok"}`))
// 	})

// 	It("when DoStandard called with a json unmarshal target (http.Response) and SetProtocolResponse(false) inflates as expected", func() {
// 		type respType struct {
// 			xnet.Response[http.Response]
// 			json.RawMessage
// 		}

// 		var respTarget respType

// 		op := ReqOpts()
// 		resp, err := ts.Client().DoStandard(context.Background(), op.UnmarshalJSONRespTo(&respTarget), op.SetProtocolResponse(false))
// 		Expect(err).To(BeNil())
// 		Expect(resp).ToNot(BeNil())
// 		Expect(resp.StatusCode).To(Equal(200))
// 		Expect(resp.Body).ToNot(BeNil())
// 		Expect(resp.Header.Get("Content-Type")).To(Equal("application/json"))
// 		Expect(bodyRequiresClose(resp)).To(BeFalse())
// 		b, err := io.ReadAll(resp.Body)
// 		Expect(err).To(BeNil())
// 		Expect(string(b)).To(Equal(`{"Status":"ok"}` + "\n"))
// 		Expect(ts.NumRoundTrips()).To(Equal(1))
// 		Expect(respTarget.ProtocolResponse()).To(BeNil())
// 		Expect(string(respTarget.RawMessage)).To(Equal(`{"Status":"ok"}`))
// 	})

// 	It("when called with a json unmarshal target and an Accept Header value inflates as expected", func() {
// 		type respType struct {
// 			xnet.Response[ClientResponse]
// 			json.RawMessage
// 		}

// 		var respTarget respType

// 		op := ReqOpts()
// 		resp, err := ts.Client().Do(context.Background(), op.UnmarshalJSONRespTo(&respTarget), op.AddHeader("Accept", "*/*"))
// 		Expect(err).To(BeNil())
// 		Expect(resp).ToNot(BeNil())
// 		Expect(resp.StatusCode).To(Equal(200))
// 		Expect(resp.Body).ToNot(BeNil())
// 		Expect(resp.Header.Get("Content-Type")).To(Equal("application/json"))
// 		Expect(bodyRequiresClose(resp)).To(BeFalse())
// 		b, err := io.ReadAll(resp.Body)
// 		Expect(err).To(BeNil())
// 		Expect(string(b)).To(Equal(`{"Status":"ok"}` + "\n"))
// 		Expect(ts.NumRoundTrips()).To(Equal(1))
// 		Expect(respTarget.ProtocolResponse()).ToNot(BeNil())
// 		Expect(string(respTarget.RawMessage)).To(Equal(`{"Status":"ok"}`))
// 	})

// 	Context("given a server that fails to respond with a json body but still sends a 200 response", func() {
// 		BeforeEach(func() {
// 			ts.SetHandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 				w.Header().Set("Content-Type", "application/json")
// 				w.WriteHeader(http.StatusOK)
// 			})
// 		})

// 		It("leads to a decode error", func() {
// 			type respType struct {
// 				xnet.Response[ClientResponse]
// 				json.RawMessage
// 			}

// 			var respTarget respType

// 			op := ReqOpts()
// 			resp, err := ts.Client().Do(context.Background(), op.UnmarshalJSONRespTo(&respTarget))
// 			Expect(err).ToNot(BeNil())
// 			Expect(errors.Is(err, ErrNoBodyInJSONResp)).To(BeTrue())
// 			Expect(resp).ToNot(BeNil())
// 			Expect(resp.StatusCode).To(Equal(200))
// 			Expect(resp.Body).ToNot(BeNil())
// 			Expect(resp.Header.Get("Content-Type")).To(Equal("application/json"))
// 			Expect(bodyRequiresClose(resp)).To(BeFalse())
// 			b, err := io.ReadAll(resp.Body)
// 			Expect(err).To(BeNil())
// 			Expect(string(b)).To(Equal(``))
// 			Expect(ts.NumRoundTrips()).To(Equal(1))
// 			Expect(respTarget.ProtocolResponse()).ToNot(BeNil())
// 			Expect(respTarget.RawMessage).To(BeNil())
// 		})
// 	})

// 	Context("given a server that fails to respond with a json Content-Type header but still sends a 200 response and body", func() {
// 		BeforeEach(func() {
// 			ts.SetHandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 				w.WriteHeader(http.StatusOK)
// 				_, _ = w.Write([]byte(`{"Status":"ok"}` + "\n"))
// 			})
// 		})

// 		It("leads to a decode error", func() {
// 			type respType struct {
// 				xnet.Response[ClientResponse]
// 				json.RawMessage
// 			}

// 			var respTarget respType

// 			op := ReqOpts()
// 			resp, err := ts.Client().Do(context.Background(), op.UnmarshalJSONRespTo(&respTarget))
// 			Expect(err).ToNot(BeNil())
// 			Expect(errors.Is(err, ErrNotJSONResp)).To(BeTrue())
// 			Expect(resp).ToNot(BeNil())
// 			Expect(resp.StatusCode).To(Equal(200))
// 			Expect(resp.Body).ToNot(BeNil())
// 			Expect(resp.Header.Get("Content-Type")).To(Equal("text/plain; charset=utf-8"))
// 			Expect(bodyRequiresClose(resp)).To(BeFalse())
// 			b, err := io.ReadAll(resp.Body)
// 			Expect(err).To(BeNil())
// 			Expect(string(b)).To(Equal(`{"Status":"ok"}` + "\n"))
// 			Expect(ts.NumRoundTrips()).To(Equal(1))
// 			Expect(respTarget.ProtocolResponse()).ToNot(BeNil())
// 			Expect(respTarget.RawMessage).To(BeNil())
// 		})
// 	})

// 	Context("given a server that fails and responds with a 500 status code", func() {
// 		BeforeEach(func() {
// 			ts.SetHandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 				w.Header().Set("Content-Type", "application/json")
// 				w.WriteHeader(http.StatusInternalServerError)
// 				_, _ = w.Write([]byte(`{"Status":"ok"}` + "\n"))
// 			})
// 		})

// 		It("leads to a decode error", func() {
// 			type respType struct {
// 				xnet.Response[ClientResponse]
// 				json.RawMessage
// 			}

// 			var respTarget respType

// 			op := ReqOpts()
// 			resp, err := ts.Client().Do(context.Background(), op.UnmarshalJSONRespTo(&respTarget), op.Retry(false))
// 			Expect(err).ToNot(BeNil())
// 			Expect(errors.Is(err, ErrRespStatusCodeNotSuccess)).To(BeTrue())
// 			Expect(resp).ToNot(BeNil())
// 			Expect(resp.StatusCode).To(Equal(http.StatusInternalServerError))
// 			Expect(resp.Body).ToNot(BeNil())
// 			Expect(resp.Header.Get("Content-Type")).To(Equal("application/json"))
// 			Expect(bodyRequiresClose(resp)).To(BeFalse())
// 			b, err := io.ReadAll(resp.Body)
// 			Expect(err).To(BeNil())
// 			Expect(string(b)).To(Equal(`{"Status":"ok"}` + "\n"))
// 			Expect(ts.NumRoundTrips()).To(Equal(1))
// 			Expect(respTarget.ProtocolResponse()).ToNot(BeNil())
// 			Expect(respTarget.RawMessage).To(BeNil())
// 		})
// 	})

// 	Context("given a server that responds with corrupted json", func() {
// 		BeforeEach(func() {
// 			ts.SetHandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 				w.Header().Set("Content-Type", "application/json")
// 				w.WriteHeader(http.StatusOK)
// 				_, _ = w.Write([]byte(`{{"Status":"ok"}` + "\n"))
// 			})
// 		})

// 		It("leads to a decode error", func() {
// 			type respType struct {
// 				xnet.Response[ClientResponse]
// 				json.RawMessage
// 			}

// 			var respTarget respType
// 			asErrTarget := &json.SyntaxError{}

// 			op := ReqOpts()
// 			resp, err := ts.Client().Do(context.Background(), op.UnmarshalJSONRespTo(&respTarget), op.Retry(false))
// 			Expect(err).ToNot(BeNil())
// 			Expect(reflect.TypeOf(err).String()).To(Equal("*json.SyntaxError"))
// 			Expect(errors.As(err, &asErrTarget)).To(BeTrue())
// 			Expect(asErrTarget.Offset).To(Equal(int64(2)))
// 			Expect(asErrTarget.Error()).To(Equal(`invalid character '{' looking for beginning of object key string`))
// 			Expect(resp).ToNot(BeNil())
// 			Expect(resp.StatusCode).To(Equal(http.StatusOK))
// 			Expect(resp.Body).ToNot(BeNil())
// 			Expect(resp.Header.Get("Content-Type")).To(Equal("application/json"))
// 			Expect(bodyRequiresClose(resp)).To(BeFalse())
// 			b, err := io.ReadAll(resp.Body)
// 			Expect(err).To(BeNil())
// 			Expect(string(b)).To(Equal(`{{"Status":"ok"}` + "\n"))
// 			Expect(ts.NumRoundTrips()).To(Equal(1))
// 			Expect(respTarget.ProtocolResponse()).ToNot(BeNil())
// 			Expect(respTarget.RawMessage).To(BeNil())
// 		})
// 	})

// })

// var _ = Context("json marshal", func() {
// 	var ts *testSetup
// 	BeforeEach(func() { ts = newSetup() })
// 	AfterEach(func() { ts.Close() })

// 	When("sending json", func() {
// 		op := ReqOpts()
// 		var opts []ReqOption
// 		BeforeEach(func() { opts = []ReqOption{op.JsonBody(json.RawMessage([]byte(`{}`)))} })
// 		AfterEach(func() {})

// 		It("sends correctly", func() {
// 			resp, err := ts.Client().Do(context.Background(), opts...)
// 			Expect(err).To(BeNil())

// 			Expect(resp).ToNot(BeNil())
// 			Expect(bodyRequiresClose(resp)).To(BeFalse())

// 			rts := ts.PopRoundTrips()
// 			Expect(len(rts)).To(Equal(1))
// 			Expect(rts[0].req.Header).To(Equal(http.Header(map[string][]string{
// 				"Content-Type":    {"application/json"},
// 				"Accept-Encoding": {rts[0].req.Header.Get("Accept-Encoding")},
// 				"User-Agent":      {rts[0].req.Header.Get("User-Agent")},
// 				"Content-Length":  {"3"},
// 			})))
// 			Expect(rts[0].req.Body.(erroredNopReader).err).To(BeNil())
// 			Expect(rts[0].req.Body.(erroredNopReader).buf.String()).To(Equal(`{}` + "\n"))
// 		})
// 	})

// 	When("sending json and not auto closing response body", func() {
// 		op := ReqOpts()
// 		var opts []ReqOption
// 		BeforeEach(func() { opts = []ReqOption{op.JsonBody(json.RawMessage(`{}`))} })
// 		AfterEach(func() {})

// 		It("sends correctly and response requires closing", func() {
// 			resp, err := ts.Client().DoStandard(context.Background(), opts...)
// 			Expect(err).To(BeNil())

// 			Expect(resp).ToNot(BeNil())
// 			Expect(bodyRequiresClose(resp)).To(BeTrue())
// 			defer resp.Body.Close()

// 			rts := ts.PopRoundTrips()
// 			Expect(len(rts)).To(Equal(1))
// 			Expect(rts[0].req.Header).To(Equal(http.Header(map[string][]string{
// 				"Content-Type":    {"application/json"},
// 				"Accept-Encoding": {rts[0].req.Header.Get("Accept-Encoding")},
// 				"User-Agent":      {rts[0].req.Header.Get("User-Agent")},
// 				"Content-Length":  {"3"},
// 			})))
// 			Expect(rts[0].req.Body.(erroredNopReader).err).To(BeNil())
// 			Expect(rts[0].req.Body.(erroredNopReader).buf.String()).To(Equal(`{}` + "\n"))
// 		})
// 	})

// 	When("sending reader in config then json", func() {
// 		op := ReqOpts()
// 		var opts []ReqOption
// 		BeforeEach(func() {
// 			opts = []ReqOption{op.Body(erroredNopReader{nil, bytes.NewBuffer(nil)}), op.JsonBody(json.RawMessage(`{}`))}
// 		})
// 		AfterEach(func() {})

// 		It("there is an error and no send attempted", func() {
// 			resp, err := ts.Client().Do(context.Background(), opts...)
// 			Expect(err).ToNot(BeNil())
// 			Expect(errors.Is(err, ErrBadConfigReaderBodyOverrides)).To(BeTrue())

// 			Expect(resp).To(BeNil())
// 			Expect(bodyRequiresClose(resp)).To(BeFalse())

// 			Expect(ts.NumRoundTrips()).To(Equal(0))
// 		})
// 	})
// })

// var _ = Context("do error tests", func() {
// 	var ts *testSetup
// 	BeforeEach(func() { ts = newSetup() })
// 	AfterEach(func() { ts.Close() })

// 	When("context expires before Do", func() {
// 		var ctx context.Context
// 		var cancel context.CancelFunc
// 		BeforeEach(func() { ctx, cancel = context.WithCancel(context.Background()) })
// 		AfterEach(func() { cancel() })

// 		It("causes Do to error without sending the request", func() {
// 			cancel()
// 			resp, err := ts.Client().Do(ctx)
// 			Expect(err).ToNot(BeNil())
// 			Expect(err).To(Equal(context.Canceled))
// 			Expect(resp).To(BeNil())
// 			Expect(bodyRequiresClose(resp)).To(BeFalse())
// 		})
// 	})

// 	When("context expires right before Do would be called", func() {
// 		BeforeEach(func() {
// 			cfg := &(ts.Client().cfg)
// 			cfg.perCallTimeout = time.Duration(1)
// 		})

// 		It("causes Do to error without sending the request", func() {
// 			resp, err := ts.Client().Do(context.Background())
// 			Expect(err).ToNot(BeNil())
// 			Expect(err).To(Equal(context.DeadlineExceeded))
// 			Expect(resp).To(BeNil())
// 			Expect(bodyRequiresClose(resp)).To(BeFalse())
// 			Expect(ts.NumRoundTrips()).To(Equal(0))
// 		})
// 	})

// 	When("render type is corrupted", func() {
// 		modifyReq := func(cfg *reqConfig) {
// 			cfg.renderType = math.MaxUint8
// 		}

// 		It("causes Do to panic without sending the request", func() {
// 			var resp *ClientResponse
// 			var err error
// 			var r any
// 			func() {
// 				defer func() {
// 					if rec := recover(); rec != nil {
// 						r = rec
// 					}
// 				}()
// 				resp, err = ts.Client().Do(context.Background(), modifyReq)
// 			}()
// 			Expect(err).To(BeNil())
// 			Expect(resp).To(BeNil())
// 			Expect(r).To(Equal("unrecognized render type: " + strconv.Itoa(math.MaxUint8)))
// 			Expect(bodyRequiresClose(resp)).To(BeFalse())
// 			Expect(ts.NumRoundTrips()).To(Equal(0))
// 		})
// 	})
// })

// var _ = Context("retry tests", func() {
// 	var ts *testSetup
// 	BeforeEach(func() { ts = newSetup() })
// 	AfterEach(func() { ts.Close() })

// 	When("server 429s with a (0) Retry-After header once", func() {
// 		BeforeEach(func() {
// 			ts.SetHandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 				ts.SetHandler(defaultHandler())
// 				w.Header().Set("Retry-After", strconv.Itoa(0))
// 				w.WriteHeader(http.StatusTooManyRequests)
// 			})
// 		})
// 		AfterEach(func() {})

// 		It("causes the client to retry its request immediately and succeed", func() {
// 			type respType struct {
// 				xnet.Response[ClientResponse]
// 				json.RawMessage
// 			}

// 			var respTarget respType
// 			op := ReqOpts()
// 			resp, err := ts.Client().Do(context.Background(), op.UnmarshalJSONRespTo(&respTarget))
// 			Expect(err).To(BeNil())
// 			Expect(resp).ToNot(BeNil())
// 			Expect(respTarget.ProtocolResponse()).ToNot(BeNil())
// 			Expect(respTarget.RawMessage).To(Equal(json.RawMessage(`{"Status":"ok"}`)))
// 			Expect(ts.NumRoundTrips()).To(Equal(2))
// 			rts := ts.PopRoundTrips()
// 			Expect(rts[1].handleStart.Sub(rts[0].handleStart) < time.Second).To(BeTrue())
// 		})
// 	})

// 	When("server 429s with a (1) Retry-After header once", func() {
// 		BeforeEach(func() {
// 			ts.SetHandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 				ts.SetHandler(defaultHandler())
// 				w.Header().Set("Retry-After", strconv.Itoa(1))
// 				w.WriteHeader(http.StatusTooManyRequests)
// 			})
// 		})
// 		AfterEach(func() {})

// 		It("causes the client to retry its request after at least a second and succeed", func() {
// 			type respType struct {
// 				xnet.Response[ClientResponse]
// 				json.RawMessage
// 			}

// 			var respTarget respType
// 			op := ReqOpts()
// 			resp, err := ts.Client().Do(context.Background(), op.UnmarshalJSONRespTo(&respTarget))
// 			Expect(err).To(BeNil())
// 			Expect(resp).ToNot(BeNil())
// 			Expect(respTarget.ProtocolResponse()).ToNot(BeNil())
// 			Expect(respTarget.RawMessage).To(Equal(json.RawMessage(`{"Status":"ok"}`)))
// 			Expect(ts.NumRoundTrips()).To(Equal(2))
// 			rts := ts.PopRoundTrips()
// 			Expect(rts[1].handleStart.Sub(rts[0].handleStart) >= time.Second).To(BeTrue())
// 			Expect(rts[1].handleStart.Sub(rts[0].handleStart) < 2*time.Second).To(BeTrue())
// 		})
// 	})

// 	When("server 429s with a (current rfc1123) Retry-After header once", func() {
// 		BeforeEach(func() {
// 			ts.SetHandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 				ts.SetHandler(defaultHandler())
// 				w.Header().Set("Retry-After", time.Now().Format(time.RFC1123))
// 				w.WriteHeader(http.StatusTooManyRequests)
// 			})
// 		})
// 		AfterEach(func() {})

// 		It("causes the client to retry its request immediately and succeed", func() {
// 			type respType struct {
// 				xnet.Response[ClientResponse]
// 				json.RawMessage
// 			}

// 			var respTarget respType
// 			op := ReqOpts()
// 			resp, err := ts.Client().Do(context.Background(), op.UnmarshalJSONRespTo(&respTarget))
// 			Expect(err).To(BeNil())
// 			Expect(resp).ToNot(BeNil())
// 			Expect(respTarget.ProtocolResponse()).ToNot(BeNil())
// 			Expect(respTarget.RawMessage).To(Equal(json.RawMessage(`{"Status":"ok"}`)))
// 			Expect(ts.NumRoundTrips()).To(Equal(2))
// 			rts := ts.PopRoundTrips()
// 			Expect(rts[1].handleStart.Sub(rts[0].handleStart) < time.Second).To(BeTrue())
// 		})
// 	})

// 	When("server 429s with a (current rfc1123 - 1s) Retry-After header once", func() {
// 		BeforeEach(func() {
// 			ts.SetHandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 				ts.SetHandler(defaultHandler())
// 				w.Header().Set("Retry-After", time.Now().Add(-time.Second).Format(time.RFC1123))
// 				w.WriteHeader(http.StatusTooManyRequests)
// 			})
// 		})
// 		AfterEach(func() {})

// 		It("causes the client to retry its request immediately and succeed", func() {
// 			type respType struct {
// 				xnet.Response[ClientResponse]
// 				json.RawMessage
// 			}

// 			var respTarget respType
// 			op := ReqOpts()
// 			resp, err := ts.Client().Do(context.Background(), op.UnmarshalJSONRespTo(&respTarget))
// 			Expect(err).To(BeNil())
// 			Expect(resp).ToNot(BeNil())
// 			Expect(respTarget.ProtocolResponse()).ToNot(BeNil())
// 			Expect(respTarget.RawMessage).To(Equal(json.RawMessage(`{"Status":"ok"}`)))
// 			Expect(ts.NumRoundTrips()).To(Equal(2))
// 			rts := ts.PopRoundTrips()
// 			Expect(rts[1].handleStart.Sub(rts[0].handleStart) < time.Second).To(BeTrue())
// 		})
// 	})

// 	When("server 429s with a (current rfc1123 + 2s) Retry-After header once", func() {
// 		// not testing with 1s because precision error range is just under a second

// 		BeforeEach(func() {
// 			ts.SetHandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 				ts.SetHandler(defaultHandler())
// 				w.Header().Set("Retry-After", time.Now().Add(2*time.Second).Format(time.RFC1123))
// 				w.WriteHeader(http.StatusTooManyRequests)
// 			})
// 		})
// 		AfterEach(func() {})

// 		It("causes the client to retry its request in a second and succeed", func() {
// 			type respType struct {
// 				xnet.Response[ClientResponse]
// 				json.RawMessage
// 			}

// 			var respTarget respType
// 			op := ReqOpts()
// 			resp, err := ts.Client().Do(context.Background(), op.UnmarshalJSONRespTo(&respTarget))
// 			Expect(err).To(BeNil())
// 			Expect(resp).ToNot(BeNil())
// 			Expect(respTarget.ProtocolResponse()).ToNot(BeNil())
// 			Expect(respTarget.RawMessage).To(Equal(json.RawMessage(`{"Status":"ok"}`)))
// 			Expect(ts.NumRoundTrips()).To(Equal(2))
// 			rts := ts.PopRoundTrips()
// 			Expect(rts[1].handleStart.Sub(rts[0].handleStart) >= time.Second).To(BeTrue())
// 			Expect(rts[1].handleStart.Sub(rts[0].handleStart) <= 3*time.Second).To(BeTrue())
// 		})
// 	})

// })
