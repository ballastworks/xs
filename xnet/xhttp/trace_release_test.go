package xhttp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ballastworks/xs/xerrors"
	"github.com/ballastworks/xs/xlog/xslog"
)

// newTraceReleaseServer builds a router behind the server's middleware chain
// (the write observer included, which the error strategy relies on) with
// one error-returning route.
func newTraceReleaseServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request) error) http.Handler {
	t.Helper()

	rt, err := NewRouter(RouterOpts().LoggerFactory(xslog.NopFactory()))
	if err != nil {
		t.Fatal(err)
	}
	rt.HandlerE(http.MethodGet, "/boom", handler)

	srv, err := NewServer(
		SrvOpts().Server(&http.Server{Addr: "127.0.0.1:0", Handler: rt}),
		SrvOpts().LoggerFactory(xslog.NopFactory()),
	)
	if err != nil {
		t.Fatal(err)
	}

	return srv
}

func serveBoom(t *testing.T, h http.Handler) int {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:0/boom", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	resp := w.Result()
	defer resp.Body.Close()
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatal(err)
	}

	return resp.StatusCode
}

// TestRouter_ReleasesTheReturnedErrorsTrace: the router owns the lifetime of
// an error a handler returns, so once the response is written the trace has
// gone back to the pool. This is the one release on the ordinary path.
func TestRouter_ReleasesTheReturnedErrorsTrace(t *testing.T) {
	traced := xerrors.WithStack(errors.New("boom"))
	if len(xerrors.Stacktrace(traced)) == 0 {
		t.Fatal("traced has no trace")
	}

	h := newTraceReleaseServer(t, func(w http.ResponseWriter, r *http.Request) error {
		return NewErrResp(
			ErrRespOpts().StatusCode(http.StatusBadRequest),
			ErrRespOpts().ErrCode("boom"),
		).With(WithErrRespOpts().LoggerDisabled(true)).CausedBy(r.Context(), traced)
	})

	if sc := serveBoom(t, h); sc != http.StatusBadRequest {
		t.Fatalf("status %d", sc)
	}
	if len(xerrors.Stacktrace(traced)) != 0 {
		t.Fatal("the router did not release the returned error's trace")
	}
}

// TestResponse_WriteRespDoesNotReleaseTheTrace: writing a response is not
// the end of the error's life (a Response can be written many times), so
// WriteResp leaves the trace to the owner.
func TestResponse_WriteRespDoesNotReleaseTheTrace(t *testing.T) {
	traced := xerrors.WithStack(errors.New("boom"))
	defer xerrors.StacktraceReleaser(traced).ReleaseStacktrace()

	er := NewErrResp(
		ErrRespOpts().StatusCode(http.StatusBadRequest),
		ErrRespOpts().ErrCode("boom"),
	).With(WithErrRespOpts().LoggerDisabled(true)).CausedBy(context.Background(), traced)

	for i := range 2 {
		w := httptest.NewRecorder()
		er.WriteResp(context.Background(), w)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("write %d: status %d", i, w.Code)
		}
		if len(xerrors.Stacktrace(traced)) == 0 {
			t.Fatalf("write %d released the trace", i)
		}
	}
}

// TestRouter_ASharedErrorLogsATraceOnEveryReturn: an error value returned
// by more than one request (a cached result, a sentinel declared with
// xerrors.New) is released after the first. CausedBy goes through
// WithStack, which takes a fresh stack when the chain has none live, so the
// second response still carries a trace and still Is the shared error.
func TestRouter_ASharedErrorLogsATraceOnEveryReturn(t *testing.T) {
	shared := xerrors.New("shared")

	var hadTrace []bool
	var isShared []bool
	h := newTraceReleaseServer(t, func(w http.ResponseWriter, r *http.Request) error {
		er := NewErrResp(
			ErrRespOpts().StatusCode(http.StatusConflict),
			ErrRespOpts().ErrCode("shared"),
		).With(WithErrRespOpts().LoggerDisabled(true)).CausedBy(r.Context(), shared)
		hadTrace = append(hadTrace, len(xerrors.Stacktrace(er)) > 0)
		isShared = append(isShared, errors.Is(er, shared))
		return er
	})

	for i := range 3 {
		if sc := serveBoom(t, h); sc != http.StatusConflict {
			t.Fatalf("request %d: status %d", i, sc)
		}
	}

	for i := range 3 {
		if !hadTrace[i] {
			t.Errorf("request %d: the response carried no stack trace", i)
		}
		if !isShared[i] {
			t.Errorf("request %d: the response's cause is no longer the shared error", i)
		}
	}
}

// TestErrResponse_CausedByTracesEveryCause: a cause that merely implements
// Unwrap (a fmt.Errorf("%w") wrapper) used to reach the logs without a
// stack because CausedBy mistook "has Unwrap" for "already traced". Every
// cause takes a stack at CausedBy unless its chain already holds a live one,
// which is left alone.
func TestErrResponse_CausedByTracesEveryCause(t *testing.T) {
	ctx := context.Background()
	resp := NewErrResp(ErrRespOpts().StatusCode(http.StatusBadRequest), ErrRespOpts().ErrCode("x"))

	plain := errors.New("plain")
	wrapped := fmt.Errorf("outer: %w", plain)
	live := xerrors.WithStack(errors.New("live"))
	defer xerrors.StacktraceReleaser(live).ReleaseStacktrace()

	for name, cause := range map[string]error{"plain": plain, "wrapped": wrapped, "live": live} {
		er := resp.CausedBy(ctx, cause)
		if len(xerrors.Stacktrace(er)) == 0 {
			t.Errorf("%s: no stack on the response", name)
		}
		if !errors.Is(er, cause) {
			t.Errorf("%s: response is not the cause", name)
		}
		if cause != live {
			xerrors.StacktraceReleaser(er).ReleaseStacktrace()
		}
	}

	if er := resp.CausedBy(ctx, live); er.Unwrap() != live {
		t.Error("a cause with a live stack was wrapped again")
	}
}
