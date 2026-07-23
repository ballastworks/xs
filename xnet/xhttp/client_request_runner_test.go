package xhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"iter"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/josephcopenhaver/tbdd-go"
	"github.com/stretchr/testify/assert"
)

var (
	errTestAuthAdd     = errors.New("test auth add failure")
	errTestAuthRefresh = errors.New("test auth refresh failure")
)

// testAuthAdder is a fully configurable AuthAdder implementation used to
// exercise every auth strategy path of the request runner. All interface
// methods count their invocations so tests can assert exactly how the runner
// drives the strategy.
type testAuthAdder struct {
	mu           sync.Mutex
	addCalls     int
	checkCalls   int
	refreshCalls int

	static           bool
	refresherEnabled bool

	// add receives the 1-based call number and the number of refreshes that
	// have completed before it.
	add     func(callNum, refreshes int, r *http.Request) (*http.Request, error)
	check   func(resp *http.Response, err error) bool
	refresh func(callNum int) error
}

func (a *testAuthAdder) AddAuthToHttpRequest(r *http.Request) (*http.Request, error) {
	a.mu.Lock()
	a.addCalls++
	callNum := a.addCalls
	refreshes := a.refreshCalls
	a.mu.Unlock()

	if a.add == nil {
		return r, nil
	}
	return a.add(callNum, refreshes, r)
}

func (a *testAuthAdder) CheckIsAuthRejectedHttpResp(resp *http.Response, err error) bool {
	a.mu.Lock()
	a.checkCalls++
	a.mu.Unlock()

	if a.check == nil {
		return false
	}
	return a.check(resp, err)
}

func (a *testAuthAdder) RefreshHttpAuth(_ context.Context) error {
	a.mu.Lock()
	a.refreshCalls++
	callNum := a.refreshCalls
	a.mu.Unlock()

	if a.refresh == nil {
		return nil
	}
	return a.refresh(callNum)
}

func (a *testAuthAdder) IsAddAuthToHttpRequestStrategyStatic() bool {
	return a.static
}

func (a *testAuthAdder) IsHttpReqAuthRefresherEnabled() bool {
	return a.refresherEnabled
}

func (a *testAuthAdder) counts() (add, check, refresh int) {
	a.mu.Lock()
	defer a.mu.Unlock()

	return a.addCalls, a.checkCalls, a.refreshCalls
}

func setAuthHeader(v string) func(int, int, *http.Request) (*http.Request, error) {
	return func(_, _ int, r *http.Request) (*http.Request, error) {
		r.Header.Set("Authorization", v)
		return r, nil
	}
}

// failOnceHandler responds once with the given status and then swaps the
// server handler back to the default success handler.
func failOnceHandler(ts *testSetup, status int, header http.Header) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ts.SetHandler(defaultHandler())
		for k, vs := range header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(status)
	}
}

func TestReqRunnerAuth(t *testing.T) {
	t.Parallel()

	type expectations struct {
		errIs      []error
		errNot     []error
		tokens     []string // nil means do not check
		rt         int
		status     int
		add        int
		refresh    int
		respNil    bool
		addIsMin   bool
		refreshMin bool
	}

	type TC struct {
		ts           *testSetup
		adder        AuthAdder
		ta           *testAuthAdder
		hdrFuncCalls *int
		opts         []ReqOption
		exp          expectations
	}

	type R struct {
		err     error
		tokens  []string
		rt      int
		status  int
		add     int
		refresh int
		respNil bool
	}

	whenF := func(t *testing.T, tc TC) R {
		ts := tc.ts
		defer ts.Close()

		op := ReqOpts()
		opts := append([]ReqOption{op.AuthAdder(tc.adder)}, tc.opts...)

		resp, err := ts.Client().Do(context.Background(), opts...)

		r := R{err: err, respNil: resp == nil, rt: ts.NumRoundTrips()}
		if resp != nil {
			r.status = resp.StatusCode
		}
		if ta := tc.ta; ta != nil {
			r.add, _, r.refresh = ta.counts()
		} else if p := tc.hdrFuncCalls; p != nil {
			r.add = *p
		}
		for _, v := range ts.PeekRoundTrips() {
			r.tokens = append(r.tokens, v.req.Header.Get("Authorization"))
		}

		return r
	}

	thenF := func(t *testing.T, tc TC, r R) {
		is := assert.New(t)

		exp := tc.exp
		if len(exp.errIs) == 0 {
			is.NoError(r.err)
		}
		for _, e := range exp.errIs {
			is.ErrorIs(r.err, e)
		}
		for _, e := range exp.errNot {
			is.NotErrorIs(r.err, e)
		}
		is.Equal(exp.rt, r.rt, "round trips")
		is.Equal(exp.respNil, r.respNil, "response nil")
		if !exp.respNil {
			is.Equal(exp.status, r.status, "status code")
		}
		if exp.addIsMin {
			is.GreaterOrEqual(r.add, exp.add, "add-auth calls")
		} else {
			is.Equal(exp.add, r.add, "add-auth calls")
		}
		if exp.refreshMin {
			is.GreaterOrEqual(r.refresh, exp.refresh, "refresh calls")
		} else {
			is.Equal(exp.refresh, r.refresh, "refresh calls")
		}
		if exp.tokens != nil {
			is.Equal(exp.tokens, r.tokens, "authorization headers seen by the server")
		}
	}

	type tci interface {
		RunI(*testing.T, int)
	}

	tcs := []tci{
		tbdd.GWT(
			TC{exp: expectations{
				rt:     1,
				status: 200,
				tokens: []string{"Bearer static-token"},
			}},
			// given
			"a static authorization header value strategy",
			func(t *testing.T, tc *TC) {
				is := assert.New(t)

				tc.ts = newSetup()

				aop := AuthAdderOpts()
				adder, err := NewAuthAdder(aop.StratAuthorizationHeaderVal("Bearer static-token"))
				is.NoError(err)
				tc.adder = adder
			},
			// when
			"the request is executed",
			whenF,
			// then
			"the server sees the static authorization header",
			thenF,
		),
		tbdd.GWT(
			TC{exp: expectations{
				rt:     2,
				status: 200,
				add:    1,
				tokens: []string{"static-tok", "static-tok"},
			}},
			// given
			"a static custom auth adder and a server that fails once with a 500",
			func(t *testing.T, tc *TC) {
				tc.ts = newSetup()
				tc.ts.SetHandlerFunc(failOnceHandler(tc.ts, http.StatusInternalServerError, nil))

				tc.ta = &testAuthAdder{static: true, add: setAuthHeader("static-tok")}
				tc.adder = tc.ta
			},
			// when
			"the request is executed and retried",
			whenF,
			// then
			"auth is resolved exactly once and reused across the retry",
			thenF,
		),
		tbdd.GWT(
			TC{exp: expectations{
				errIs:   []error{ErrStaticAuthFailed, errTestAuthAdd},
				respNil: true,
				add:     1,
			}},
			// given
			"a static auth strategy that errors",
			func(t *testing.T, tc *TC) {
				tc.ts = newSetup()

				tc.ta = &testAuthAdder{
					static: true,
					add: func(_, _ int, _ *http.Request) (*http.Request, error) {
						return nil, errTestAuthAdd
					},
				}
				tc.adder = tc.ta
			},
			// when
			"the request is executed",
			whenF,
			// then
			"a static auth error is returned without any round trips",
			thenF,
		),
		tbdd.GWT(
			TC{exp: expectations{
				rt:     2,
				status: 200,
				add:    2,
				tokens: []string{"t1", "t2"},
			}},
			// given
			"a dynamic authorization header func strategy and a server that fails once with a 500",
			func(t *testing.T, tc *TC) {
				is := assert.New(t)

				tc.ts = newSetup()
				tc.ts.SetHandlerFunc(failOnceHandler(tc.ts, http.StatusInternalServerError, nil))

				calls := 0
				tc.hdrFuncCalls = &calls

				aop := AuthAdderOpts()
				adder, err := NewAuthAdder(aop.StratAuthorizationHeaderFunc(func() (string, error) {
					calls++
					return "t" + strconv.Itoa(calls), nil
				}))
				is.NoError(err)
				tc.adder = adder
			},
			// when
			"the request is executed and retried",
			whenF,
			// then
			"auth is re-resolved for every attempt",
			thenF,
		),
		tbdd.GWT(
			TC{exp: expectations{
				errIs:   []error{ErrAddAuthFailed, errTestAuthAdd},
				respNil: true,
				add:     1,
			}},
			// given
			"a dynamic authorization header func strategy that errors",
			func(t *testing.T, tc *TC) {
				is := assert.New(t)

				tc.ts = newSetup()

				calls := 0
				tc.hdrFuncCalls = &calls

				aop := AuthAdderOpts()
				adder, err := NewAuthAdder(aop.StratAuthorizationHeaderFunc(func() (string, error) {
					calls++
					return "", errTestAuthAdd
				}))
				is.NoError(err)
				tc.adder = adder
			},
			// when
			"the request is executed",
			whenF,
			// then
			"an add-auth error is returned after a single resolution attempt and no round trips",
			thenF,
		),
		tbdd.GWT(
			TC{exp: expectations{
				errIs:   []error{ErrAddAuthFailed, errTestAuthAdd},
				respNil: true,
				add:     1,
			}},
			// given
			"a dynamic custom auth adder that errors and has auth refreshing disabled",
			func(t *testing.T, tc *TC) {
				tc.ts = newSetup()

				tc.ta = &testAuthAdder{
					refresherEnabled: false,
					add: func(_, _ int, _ *http.Request) (*http.Request, error) {
						return nil, errTestAuthAdd
					},
				}
				tc.adder = tc.ta
			},
			// when
			"the request is executed",
			whenF,
			// then
			"the add-auth error is terminal with no refresh-driven retries",
			thenF,
		),
		tbdd.GWT(
			TC{exp: expectations{
				rt:      1,
				status:  200,
				add:     2,
				refresh: 1,
				tokens:  []string{"fresh-tok"},
			}},
			// given
			"a dynamic custom auth adder that errors until auth is refreshed",
			func(t *testing.T, tc *TC) {
				tc.ts = newSetup()

				tc.ta = &testAuthAdder{
					refresherEnabled: true,
					add: func(_, refreshes int, r *http.Request) (*http.Request, error) {
						if refreshes == 0 {
							return nil, errTestAuthAdd
						}
						r.Header.Set("Authorization", "fresh-tok")
						return r, nil
					},
				}
				tc.adder = tc.ta
			},
			// when
			"the request is executed",
			whenF,
			// then
			"the runner refreshes auth, re-adds it, and succeeds with a single round trip",
			thenF,
		),
		tbdd.GWT(
			TC{exp: expectations{
				errIs:      []error{ErrAddAuthFailed, errTestAuthAdd},
				respNil:    true,
				add:        2,
				addIsMin:   true,
				refresh:    1,
				refreshMin: true,
			}},
			// given
			"a dynamic custom auth adder that errors persistently despite auth refreshes",
			func(t *testing.T, tc *TC) {
				tc.ts = newSetup()

				tc.ta = &testAuthAdder{
					refresherEnabled: true,
					add: func(_, _ int, _ *http.Request) (*http.Request, error) {
						return nil, errTestAuthAdd
					},
				}
				tc.adder = tc.ta

				op := ReqOpts()
				tc.opts = []ReqOption{op.RetryMaxTimeout(500 * time.Millisecond)}
			},
			// when
			"the request is executed until the retry deadline",
			whenF,
			// then
			"the add-auth error is returned, refreshes were attempted, and the server was never called",
			thenF,
		),
		tbdd.GWT(
			TC{exp: expectations{
				rt:      2,
				status:  200,
				add:     2,
				refresh: 1,
				tokens:  []string{"stale-tok", "new-tok"},
			}},
			// given
			"a server that rejects the initial token with a 401 and a refresher that provides a new token",
			func(t *testing.T, tc *TC) {
				tc.ts = newSetup()
				tc.ts.SetHandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Header.Get("Authorization") != "new-tok" {
						w.WriteHeader(http.StatusUnauthorized)
						return
					}
					defaultHandler().ServeHTTP(w, r)
				})

				tc.ta = &testAuthAdder{
					refresherEnabled: true,
					add: func(_, refreshes int, r *http.Request) (*http.Request, error) {
						tok := "stale-tok"
						if refreshes > 0 {
							tok = "new-tok"
						}
						r.Header.Set("Authorization", tok)
						return r, nil
					},
					check: func(resp *http.Response, _ error) bool {
						return resp != nil && resp.StatusCode == http.StatusUnauthorized
					},
				}
				tc.adder = tc.ta
			},
			// when
			"the request is executed",
			whenF,
			// then
			"the 401 triggers an auth refresh and the retry succeeds with the new token",
			thenF,
		),
		tbdd.GWT(
			TC{exp: expectations{
				errIs:      []error{ErrAddAuthFailed, ErrRespStatusCodeNotSuccess},
				status:     http.StatusUnauthorized,
				rt:         1,
				add:        2,
				addIsMin:   true,
				refresh:    1,
				refreshMin: true,
			}},
			// given
			"a server that always responds 401 and an adder that errors after the auth refresh",
			func(t *testing.T, tc *TC) {
				tc.ts = newSetup()
				tc.ts.SetHandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusUnauthorized)
				})

				tc.ta = &testAuthAdder{
					refresherEnabled: true,
					add: func(_, refreshes int, r *http.Request) (*http.Request, error) {
						if refreshes > 0 {
							return nil, errTestAuthAdd
						}
						r.Header.Set("Authorization", "stale-tok")
						return r, nil
					},
					check: func(resp *http.Response, _ error) bool {
						return resp != nil && resp.StatusCode == http.StatusUnauthorized
					},
				}
				tc.adder = tc.ta

				op := ReqOpts()
				tc.opts = []ReqOption{op.RetryMaxTimeout(500 * time.Millisecond)}
			},
			// when
			"the request is executed until the retry deadline",
			whenF,
			// then
			"the first 401 error response is returned along with the add-auth error",
			thenF,
		),
		tbdd.GWT(
			TC{exp: expectations{
				errIs: []error{ErrRespStatusCodeNotSuccess},
				// The refresh error is currently superseded by the first
				// error response; this documents present behavior.
				errNot:  []error{errTestAuthRefresh},
				status:  http.StatusUnauthorized,
				rt:      1,
				add:     1,
				refresh: 1,
				tokens:  []string{"stale-tok"},
			}},
			// given
			"a server that always responds 401 and an auth refresher that errors",
			func(t *testing.T, tc *TC) {
				tc.ts = newSetup()
				tc.ts.SetHandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusUnauthorized)
				})

				tc.ta = &testAuthAdder{
					refresherEnabled: true,
					add:              setAuthHeader("stale-tok"),
					check: func(resp *http.Response, _ error) bool {
						return resp != nil && resp.StatusCode == http.StatusUnauthorized
					},
					refresh: func(_ int) error {
						return errTestAuthRefresh
					},
				}
				tc.adder = tc.ta
			},
			// when
			"the request is executed",
			whenF,
			// then
			"the first 401 error response wins over the refresh failure",
			thenF,
		),
	}

	for i, v := range tcs {
		v.RunI(t, i)
	}
}

func TestReqRunnerRetryPolicy(t *testing.T) {
	t.Parallel()

	type TC struct {
		ts     *testSetup
		method string
		hdrK   string
		hdrV   string
		expRT  int
		expOK  bool
	}

	type R struct {
		err    error
		rt     int
		status int
	}

	b := tbdd.GWT(
		TC{method: http.MethodGet, expRT: 2, expOK: true},
		// given
		"a server that fails once with a 500 and then succeeds",
		func(t *testing.T, tc *TC) {
			tc.ts = newSetup()
			tc.ts.SetHandlerFunc(failOnceHandler(tc.ts, http.StatusInternalServerError, nil))
		},
		// when
		"a request with the variant method and headers is executed",
		func(t *testing.T, tc TC) R {
			ts := tc.ts
			defer ts.Close()

			op := ReqOpts()
			opts := []ReqOption{op.Method(tc.method)}
			if tc.hdrK != "" {
				opts = append(opts, op.HeaderValue(tc.hdrK, tc.hdrV))
			}

			resp, err := ts.Client().Do(context.Background(), opts...)

			r := R{err: err, rt: ts.NumRoundTrips()}
			if resp != nil {
				r.status = resp.StatusCode
			}
			return r
		},
		// then
		"the request is retried only when idempotent by method or by idempotency header",
		func(t *testing.T, tc TC, r R) {
			is := assert.New(t)

			if tc.expOK {
				is.NoError(r.err)
				is.Equal(http.StatusOK, r.status)
			} else {
				is.ErrorIs(r.err, ErrRespStatusCodeNotSuccess)
				is.Equal(http.StatusInternalServerError, r.status)
			}
			is.Equal(tc.expRT, r.rt, "round trips")
		},
	)

	b.Variants = func(_ *testing.T, _ TC) iter.Seq[tbdd.TestVariant[TC]] {
		return slices.Values([]tbdd.TestVariant[TC]{
			{Kind: "HEAD", TC: TC{method: http.MethodHead, expRT: 2, expOK: true}},
			{Kind: "PUT", TC: TC{method: http.MethodPut, expRT: 2, expOK: true}},
			{Kind: "DELETE", TC: TC{method: http.MethodDelete, expRT: 2, expOK: true}},
			{Kind: "OPTIONS", TC: TC{method: http.MethodOptions, expRT: 2, expOK: true}},
			{Kind: "POST", TC: TC{method: http.MethodPost, expRT: 1, expOK: false}},
			{Kind: "PATCH", TC: TC{method: http.MethodPatch, expRT: 1, expOK: false}},
			{Kind: "POST+Idempotency-Key", TC: TC{method: http.MethodPost, hdrK: "Idempotency-Key", hdrV: "k1", expRT: 2, expOK: true}},
			{Kind: "POST+X-Idempotency-Key", TC: TC{method: http.MethodPost, hdrK: "X-Idempotency-Key", hdrV: "k1", expRT: 2, expOK: true}},
		})
	}

	b.Run(t)
}

func TestReqRunnerRetryBehaviors(t *testing.T) {
	t.Parallel()

	type TC struct {
		ts   *testSetup
		opts func(op reqOpts) []ReqOption

		expErrIs   []error
		expBody    string // "" means do not check
		expMarker  string // "" means do not check
		expStatus  int
		expRT      int
		expRTIsMin bool
		expRespNil bool
	}

	type R struct {
		err     error
		body    string
		marker  string
		status  int
		rt      int
		respNil bool
	}

	whenF := func(t *testing.T, tc TC) R {
		ts := tc.ts
		defer ts.Close()

		op := ReqOpts()
		var opts []ReqOption
		if tc.opts != nil {
			opts = tc.opts(op)
		}

		resp, err := ts.Client().Do(context.Background(), opts...)

		r := R{err: err, respNil: resp == nil, rt: ts.NumRoundTrips()}
		if resp != nil {
			r.status = resp.StatusCode
			r.marker = resp.Header.Get("Marker")
			if resp.Body != nil {
				b, err := io.ReadAll(resp.Body)
				assert.New(t).NoError(err)
				r.body = string(b)
			}
		}
		return r
	}

	thenF := func(t *testing.T, tc TC, r R) {
		is := assert.New(t)

		if len(tc.expErrIs) == 0 {
			is.NoError(r.err)
		}
		for _, e := range tc.expErrIs {
			is.ErrorIs(r.err, e)
		}
		is.Equal(tc.expRespNil, r.respNil, "response nil")
		if !tc.expRespNil {
			is.Equal(tc.expStatus, r.status, "status code")
		}
		if tc.expRTIsMin {
			is.GreaterOrEqual(r.rt, tc.expRT, "round trips")
		} else {
			is.Equal(tc.expRT, r.rt, "round trips")
		}
		if tc.expBody != "" {
			is.Equal(tc.expBody, r.body, "response body")
		}
		if tc.expMarker != "" {
			is.Equal(tc.expMarker, r.marker, "response marker header")
		}
	}

	type tci interface {
		RunI(*testing.T, int)
	}

	tcs := []tci{
		tbdd.GWT(
			TC{
				opts: func(op reqOpts) []ReqOption {
					return []ReqOption{op.Method(http.MethodPost)}
				},
				expStatus: http.StatusOK,
				expRT:     2,
			},
			// given
			"a server that responds 503 with a Retry-After of zero once and then succeeds",
			func(t *testing.T, tc *TC) {
				tc.ts = newSetup()
				tc.ts.SetHandlerFunc(failOnceHandler(tc.ts, http.StatusServiceUnavailable, http.Header{"Retry-After": {"0"}}))
			},
			// when
			"a non-idempotent POST request is executed",
			whenF,
			// then
			"the Retry-After response header drives a retry even for a non-idempotent method",
			thenF,
		),
		tbdd.GWT(
			TC{
				opts: func(op reqOpts) []ReqOption {
					return []ReqOption{op.Retry(false)}
				},
				expErrIs:  []error{ErrRespStatusCodeNotSuccess},
				expStatus: http.StatusInternalServerError,
				expRT:     1,
			},
			// given
			"a server that always returns 500 and a request with retries disabled",
			func(t *testing.T, tc *TC) {
				tc.ts = newSetup()
				tc.ts.SetHandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
				})
			},
			// when
			"the request is executed",
			whenF,
			// then
			"exactly one attempt is made and the error response is returned",
			thenF,
		),
		tbdd.GWT(
			TC{
				opts: func(op reqOpts) []ReqOption {
					return []ReqOption{op.RetryMaxTimeout(300 * time.Millisecond)}
				},
				expErrIs:   []error{ErrRespStatusCodeNotSuccess},
				expStatus:  http.StatusInternalServerError,
				expRT:      2,
				expRTIsMin: true,
			},
			// given
			"a server that always returns 500 and a short retry deadline",
			func(t *testing.T, tc *TC) {
				tc.ts = newSetup()
				tc.ts.SetHandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
				})
			},
			// when
			"the request is executed until the retry deadline",
			whenF,
			// then
			"retries stop at the deadline and the error response is returned",
			thenF,
		),
		tbdd.GWT(
			TC{
				opts: func(op reqOpts) []ReqOption {
					return []ReqOption{op.RetryMaxTimeout(300 * time.Millisecond)}
				},
				expErrIs:   []error{ErrRespStatusCodeNotSuccess},
				expStatus:  http.StatusInternalServerError,
				expBody:    "first-failure",
				expMarker:  "first",
				expRT:      2,
				expRTIsMin: true,
			},
			// given
			"a server that fails with a marked 500 and then differently-marked 502s until the retry deadline",
			func(t *testing.T, tc *TC) {
				tc.ts = newSetup()

				later := func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Marker", "later")
					w.WriteHeader(http.StatusBadGateway)
					_, _ = w.Write([]byte("later-failure"))
				}
				tc.ts.SetHandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					tc.ts.SetHandlerFunc(later)
					w.Header().Set("Marker", "first")
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte("first-failure"))
				})
			},
			// when
			"the request is executed until the retry deadline",
			whenF,
			// then
			"the first observed error response is the one returned",
			thenF,
		),
		tbdd.GWT(
			TC{
				opts: func(op reqOpts) []ReqOption {
					return []ReqOption{op.PerCallTimeout(50 * time.Millisecond)}
				},
				expErrIs:   []error{context.DeadlineExceeded},
				expRespNil: true,
				expRT:      1,
			},
			// given
			"a server that hangs beyond the per-call timeout",
			func(t *testing.T, tc *TC) {
				tc.ts = newSetup()
				tc.ts.SetHandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					select {
					case <-r.Context().Done():
					case <-time.After(3 * time.Second):
					}
					w.WriteHeader(http.StatusOK)
				})
			},
			// when
			"the request is executed",
			whenF,
			// then
			"the per-call deadline error is returned without a retry",
			thenF,
		),
	}

	for i, v := range tcs {
		v.RunI(t, i)
	}
}

func TestReqRunnerBodyResend(t *testing.T) {
	t.Parallel()

	const payload = "hello resend"

	type TC struct {
		ts      *testSetup
		mkOpt   func(op reqOpts) ReqOption
		payload string
		expCT   string // "" means do not check
	}

	type R struct {
		err    error
		status int
		rt     int
		bodies []string
		cts    []string
		echo   string
	}

	b := tbdd.GWT(
		TC{
			payload: `{"K":"v"}` + "\n",
			expCT:   "application/json",
			mkOpt: func(op reqOpts) ReqOption {
				return op.JsonBody(struct{ K string }{"v"})
			},
		},
		// given
		"a server that fails once with a 500 and then echoes the request body",
		func(t *testing.T, tc *TC) {
			tc.ts = newSetup()

			echo := func(w http.ResponseWriter, r *http.Request) {
				b, err := io.ReadAll(r.Body)
				if err != nil {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				w.Header().Set("Content-Type", "text/plain")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(b)
			}
			tc.ts.SetHandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				tc.ts.SetHandlerFunc(echo)
				w.WriteHeader(http.StatusInternalServerError)
			})
		},
		// when
		"an idempotency-annotated POST request with the variant body kind is executed",
		func(t *testing.T, tc TC) R {
			ts := tc.ts
			defer ts.Close()

			op := ReqOpts()
			opts := []ReqOption{
				op.Method(http.MethodPost),
				op.HeaderValue("Idempotency-Key", "k1"),
				tc.mkOpt(op),
			}

			resp, err := ts.Client().Do(context.Background(), opts...)

			r := R{err: err, rt: ts.NumRoundTrips()}
			if resp != nil {
				r.status = resp.StatusCode
				if resp.Body != nil {
					b, err := io.ReadAll(resp.Body)
					assert.New(t).NoError(err)
					r.echo = string(b)
				}
			}
			for _, v := range ts.PeekRoundTrips() {
				b, err := io.ReadAll(v.req.Body)
				assert.New(t).NoError(err)
				r.bodies = append(r.bodies, string(b))
				r.cts = append(r.cts, v.req.Header.Get("Content-Type"))
			}
			return r
		},
		// then
		"the request body is resent identically on the retry and echoed back",
		func(t *testing.T, tc TC, r R) {
			is := assert.New(t)

			is.NoError(r.err)
			is.Equal(http.StatusOK, r.status)
			is.Equal(2, r.rt, "round trips")
			is.Equal([]string{tc.payload, tc.payload}, r.bodies, "request bodies seen by the server")
			is.Equal(tc.payload, r.echo, "echoed response body")
			if tc.expCT != "" {
				is.Equal([]string{tc.expCT, tc.expCT}, r.cts, "request content types")
			}
		},
	)

	b.Variants = func(_ *testing.T, _ TC) iter.Seq[tbdd.TestVariant[TC]] {
		return slices.Values([]tbdd.TestVariant[TC]{
			{Kind: "bytes.Buffer", TC: TC{payload: payload, mkOpt: func(op reqOpts) ReqOption {
				return op.Body(bytes.NewBufferString(payload))
			}}},
			{Kind: "bytes.Reader", TC: TC{payload: payload, mkOpt: func(op reqOpts) ReqOption {
				return op.Body(bytes.NewReader([]byte(payload)))
			}}},
			{Kind: "strings.Reader", TC: TC{payload: payload, mkOpt: func(op reqOpts) ReqOption {
				return op.Body(strings.NewReader(payload))
			}}},
			{Kind: "opaque io.Reader", TC: TC{payload: payload, mkOpt: func(op reqOpts) ReqOption {
				return op.Body(struct{ io.Reader }{strings.NewReader(payload)})
			}}},
			{Kind: "BodyAndGetter", TC: TC{payload: payload, mkOpt: func(op reqOpts) ReqOption {
				return op.BodyAndGetter(strings.NewReader(payload), func() (io.ReadCloser, error) {
					return io.NopCloser(strings.NewReader(payload)), nil
				})
			}}},
		})
	}

	b.Run(t)
}

func TestReqRunnerJSONResponse(t *testing.T) {
	t.Parallel()

	type jsonTarget struct {
		Status string
	}

	type expectations struct {
		errIs     []error
		errNot    []error
		target    string
		syntaxErr bool
	}

	type TC struct {
		ts         *testSetup
		respStatus int
		respCT     string
		respBody   string
		exp        expectations
	}

	type R struct {
		err    error
		accept string
		target string
		status int
		rt     int
	}

	b := tbdd.GWT(
		TC{
			respStatus: http.StatusOK,
			respCT:     "application/json",
			respBody:   `{"Status":"ok"}`,
			exp:        expectations{target: "ok"},
		},
		// given
		"a server that responds with the variant status, content type, and body",
		func(t *testing.T, tc *TC) {
			tc.ts = newSetup()

			respStatus, respCT, respBody := tc.respStatus, tc.respCT, tc.respBody
			tc.ts.SetHandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if respCT != "" {
					w.Header().Set("Content-Type", respCT)
				}
				w.WriteHeader(respStatus)
				if respBody != "" {
					_, _ = w.Write([]byte(respBody))
				}
			})
		},
		// when
		"a request with a json unmarshal target is executed",
		func(t *testing.T, tc TC) R {
			ts := tc.ts
			defer ts.Close()

			var target jsonTarget

			op := ReqOpts()
			resp, err := ts.Client().Do(context.Background(), op.UnmarshalJSONRespTo(&target))

			r := R{err: err, target: target.Status, rt: ts.NumRoundTrips()}
			if resp != nil {
				r.status = resp.StatusCode
			}
			if rts := ts.PeekRoundTrips(); len(rts) > 0 {
				r.accept = rts[0].req.Header.Get("Accept")
			}
			return r
		},
		// then
		"the decode outcome and error sentinels match the documented behavior",
		func(t *testing.T, tc TC, r R) {
			is := assert.New(t)

			exp := tc.exp
			if len(exp.errIs) == 0 && !exp.syntaxErr {
				is.NoError(r.err)
			}
			for _, e := range exp.errIs {
				is.ErrorIs(r.err, e)
			}
			for _, e := range exp.errNot {
				is.NotErrorIs(r.err, e)
			}
			if exp.syntaxErr {
				var syntaxErr *json.SyntaxError
				is.ErrorAs(r.err, &syntaxErr)
			}
			is.Equal(exp.target, r.target, "unmarshal target")
			is.Equal(tc.respStatus, r.status, "status code")
			is.Equal(1, r.rt, "round trips")
			is.Equal("application/json", r.accept, "request accept header")
		},
	)

	b.Variants = func(_ *testing.T, _ TC) iter.Seq[tbdd.TestVariant[TC]] {
		return slices.Values([]tbdd.TestVariant[TC]{
			{Kind: "json content type with parameters", TC: TC{
				respStatus: http.StatusOK,
				respCT:     "application/json; charset=utf-8",
				respBody:   `{"Status":"ok"}`,
				exp:        expectations{target: "ok"},
			}},
			{Kind: "non-json content type", TC: TC{
				respStatus: http.StatusOK,
				respCT:     "text/plain",
				respBody:   `{"Status":"ok"}`,
				exp:        expectations{errIs: []error{ErrNotJSONResp}},
			}},
			{Kind: "empty json body", TC: TC{
				respStatus: http.StatusOK,
				respCT:     "application/json",
				respBody:   "",
				exp:        expectations{errIs: []error{ErrNoBodyInJSONResp}},
			}},
			{Kind: "malformed json body", TC: TC{
				respStatus: http.StatusOK,
				respCT:     "application/json",
				respBody:   "{",
				exp:        expectations{syntaxErr: true},
			}},
			{Kind: "error status with valid json", TC: TC{
				respStatus: http.StatusBadRequest,
				respCT:     "application/json",
				respBody:   `{"Status":"bad"}`,
				exp: expectations{
					errIs:  []error{ErrRespStatusCodeNotSuccess},
					errNot: []error{ErrNotJSONResp},
					target: "bad",
				},
			}},
			{Kind: "error status with non-json body", TC: TC{
				respStatus: http.StatusBadRequest,
				respCT:     "text/plain",
				respBody:   "nope",
				exp: expectations{
					errIs: []error{ErrRespStatusCodeNotSuccess, ErrNotJSONResp},
				},
			}},
		})
	}

	b.Run(t)
}

func TestReqRunnerPreflight(t *testing.T) {
	t.Parallel()

	type TC struct {
		ts *testSetup
		// mkClient overrides the setup's client when non-nil so a variant can
		// exercise client-level misconfiguration.
		mkClient  func(t *testing.T, ts *testSetup) *Client
		mkOpts    func(op reqOpts) []ReqOption
		cancelCtx bool
		expErrIs  error
	}

	type R struct {
		err     error
		rt      int
		respNil bool
	}

	b := tbdd.GWT(
		TC{
			mkOpts: func(op reqOpts) []ReqOption {
				return []ReqOption{op.Method("@")}
			},
			expErrIs: ErrBadMethod,
		},
		// given
		"a client pointed at a live server",
		func(t *testing.T, tc *TC) {
			tc.ts = newSetup()
		},
		// when
		"a request with the variant invalid configuration is executed",
		func(t *testing.T, tc TC) R {
			ts := tc.ts
			defer ts.Close()

			ctx := context.Background()
			if tc.cancelCtx {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}

			op := ReqOpts()
			var opts []ReqOption
			if tc.mkOpts != nil {
				opts = tc.mkOpts(op)
			}

			c := ts.Client()
			if tc.mkClient != nil {
				c = tc.mkClient(t, ts)
			}

			resp, err := c.Do(ctx, opts...)
			return R{err: err, respNil: resp == nil, rt: ts.NumRoundTrips()}
		},
		// then
		"the documented error is returned and the server is never called",
		func(t *testing.T, tc TC, r R) {
			is := assert.New(t)

			is.ErrorIs(r.err, tc.expErrIs)
			is.True(r.respNil, "response nil")
			is.Zero(r.rt, "round trips")
		},
	)

	b.Variants = func(_ *testing.T, _ TC) iter.Seq[tbdd.TestVariant[TC]] {
		return slices.Values([]tbdd.TestVariant[TC]{
			{Kind: "nil unmarshal target", TC: TC{
				mkOpts: func(op reqOpts) []ReqOption {
					return []ReqOption{op.UnmarshalJSONRespTo(nil)}
				},
				expErrIs: ErrNilUnmarshalTarget,
			}},
			{Kind: "nil GetBody while retrying", TC: TC{
				mkOpts: func(op reqOpts) []ReqOption {
					return []ReqOption{op.BodyAndGetter(strings.NewReader("x"), nil)}
				},
				expErrIs: ErrNilGetBodyNotAllowedWhenRetrying,
			}},
			{Kind: "non-positive per-call timeout", TC: TC{
				mkOpts: func(op reqOpts) []ReqOption {
					return []ReqOption{op.PerCallTimeout(-1)}
				},
				expErrIs: ErrPerCallTimeoutMustBeGTZero,
			}},
			{Kind: "base url with query", TC: TC{
				mkClient: func(t *testing.T, ts *testSetup) *Client {
					is := assert.New(t)

					op := ClientOpts()
					c, err := NewClient(op.BaseURL(ts.BaseURL() + "/?b=c"))
					is.NoError(err)
					return c
				},
				expErrIs: errBadPath,
			}},
			{Kind: "canceled context", TC: TC{
				cancelCtx: true,
				expErrIs:  context.Canceled,
			}},
		})
	}

	b.Run(t)

	{
		type TC struct {
			ts *testSetup
		}

		type R struct {
			panicVal any
			rt       int
		}

		tbdd.WT(
			TC{ts: newSetup()},
			// when
			"a nil request option is passed to Do",
			func(t *testing.T, tc TC) R {
				ts := tc.ts
				defer ts.Close()

				var panicVal any
				func() {
					defer func() {
						panicVal = recover()
					}()
					_, _ = ts.Client().Do(context.Background(), nil)
				}()

				return R{panicVal: panicVal, rt: ts.NumRoundTrips()}
			},
			// then
			"do panics with the documented message and the server is never called",
			func(t *testing.T, tc TC, r R) {
				is := assert.New(t)

				is.Equal("nil option in do call at index 0", r.panicVal)
				is.Zero(r.rt, "round trips")
			},
		).Run(t)
	}
}

func TestReqRunnerResponseLifecycle(t *testing.T) {
	t.Parallel()

	const respPayload = "response payload"

	type TC struct {
		ts         *testSetup
		useStd     bool
		respStatus int
		expErrIs   []error
		// expRequiresClose documents which flows hand the caller a live
		// (connection-backed) body versus a fully buffered one.
		expRequiresClose bool
	}

	type R struct {
		err           error
		status        int
		rt            int
		body          string
		requiresClose bool
	}

	b := tbdd.GWT(
		TC{respStatus: http.StatusOK},
		// given
		"a server that responds with the variant status and a fixed body",
		func(t *testing.T, tc *TC) {
			tc.ts = newSetup()

			respStatus := tc.respStatus
			tc.ts.SetHandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/plain")
				w.WriteHeader(respStatus)
				_, _ = w.Write([]byte(respPayload))
			})
		},
		// when
		"the request is executed via the variant Do flavor",
		func(t *testing.T, tc TC) R {
			ts := tc.ts
			defer ts.Close()

			is := assert.New(t)
			r := R{}

			if tc.useStd {
				resp, err := ts.Client().DoStandard(context.Background())
				r.err = err
				if !is.NotNil(resp) {
					return r
				}
				r.status = resp.StatusCode
				r.requiresClose = bodyRequiresClose(resp)
				b, readErr := io.ReadAll(resp.Body)
				is.NoError(readErr)
				is.NoError(resp.Body.Close())
				r.body = string(b)
			} else {
				resp, err := ts.Client().Do(context.Background())
				r.err = err
				if !is.NotNil(resp) {
					return r
				}
				r.status = resp.StatusCode
				r.requiresClose = bodyRequiresClose(resp)
				b, readErr := io.ReadAll(resp.Body)
				is.NoError(readErr)
				r.body = string(b)
			}
			r.rt = ts.NumRoundTrips()

			return r
		},
		// then
		"the response body buffering matches the documented Do vs DoStandard contract",
		func(t *testing.T, tc TC, r R) {
			is := assert.New(t)

			if len(tc.expErrIs) == 0 {
				is.NoError(r.err)
			}
			for _, e := range tc.expErrIs {
				is.ErrorIs(r.err, e)
			}
			is.Equal(tc.respStatus, r.status, "status code")
			is.Equal(1, r.rt, "round trips")
			is.Equal(respPayload, r.body, "response body")
			is.Equal(tc.expRequiresClose, r.requiresClose, "body requires close")
		},
	)

	b.Variants = func(_ *testing.T, _ TC) iter.Seq[tbdd.TestVariant[TC]] {
		return slices.Values([]tbdd.TestVariant[TC]{
			{Kind: "Do with error status", TC: TC{
				respStatus: http.StatusBadRequest,
				expErrIs:   []error{ErrRespStatusCodeNotSuccess},
			}},
			{Kind: "DoStandard success", TC: TC{
				useStd:           true,
				respStatus:       http.StatusOK,
				expRequiresClose: true,
			}},
			{Kind: "DoStandard with non-retryable error status", TC: TC{
				useStd:           true,
				respStatus:       http.StatusBadRequest,
				expErrIs:         []error{ErrRespStatusCodeNotSuccess},
				expRequiresClose: true,
			}},
		})
	}

	b.Run(t)
}
