package xhttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type spanTestFailingAuthAdder struct{}

func (spanTestFailingAuthAdder) AddAuthToHttpRequest(*http.Request) (*http.Request, error) {
	return nil, errors.New("no credentials")
}

func (spanTestFailingAuthAdder) CheckIsAuthRejectedHttpResp(*http.Response, error) bool {
	return false
}

func (spanTestFailingAuthAdder) RefreshHttpAuth(context.Context) error {
	return nil
}

// TestClientRequestSpansEnded ensures every span a client request starts is
// ended, and that the xhttp.request.do span ends with a status reflecting the
// request outcome before the teardown span starts.
//
// Not parallel since it replaces the global tracer provider the client uses.
func TestClientRequestSpansEnded(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	old := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(old)
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fail" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	c, err := NewClient(ClientOpts().BaseURL(srv.URL))
	require.NoError(t, err)

	tests := []struct {
		name      string
		opts      []ReqOption
		expErr    bool
		expStatus codes.Code
	}{
		{"success", []ReqOption{ReqOpts().Path("/ok"), ReqOpts().Retry(false)}, false, codes.Ok},
		{"success with retries", []ReqOption{ReqOpts().Path("/ok"), ReqOpts().Retry(true)}, false, codes.Ok},
		{"error status", []ReqOption{ReqOpts().Path("/fail"), ReqOpts().Retry(false)}, true, codes.Error},
		{"error status with retries", []ReqOption{ReqOpts().Path("/fail"), ReqOpts().Retry(true)}, true, codes.Error},
		{"auth failure", []ReqOption{ReqOpts().Path("/ok"), ReqOpts().Retry(false), ReqOpts().AuthAdder(spanTestFailingAuthAdder{})}, true, codes.Error},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sr.Reset()

			_, err := c.Do(context.Background(), tc.opts...)
			if tc.expErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			for _, s := range sr.Started() {
				require.False(t, s.EndTime().IsZero(), "span %q must be ended", s.Name())
			}

			spans := map[string]sdktrace.ReadOnlySpan{}
			for _, s := range sr.Ended() {
				spans[s.Name()] = s
			}

			do, ok := spans["xhttp.request.do"]
			require.True(t, ok, "xhttp.request.do span must be ended")
			require.Equal(t, tc.expStatus, do.Status().Code)

			teardown, ok := spans["xhttp.request.teardown"]
			require.True(t, ok, "xhttp.request.teardown span must be ended")
			require.False(t, do.EndTime().After(teardown.StartTime()), "xhttp.request.do must end before teardown starts")
		})
	}
}

// TestClientResponseErrorsRecorded ensures errors found while processing a
// response, after its attempt span has ended, are recorded on the
// xhttp.request.do span rather than lost on the ended attempt span.
//
// Not parallel since it replaces the global tracer provider the client uses.
func TestClientResponseErrorsRecorded(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	old := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(old)
	})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bad-status":
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("nope"))
		case "/bad-json":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("{"))
		}
	}))
	t.Cleanup(srv.Close)

	c, err := NewClient(ClientOpts().BaseURL(srv.URL))
	require.NoError(t, err)

	tests := []struct {
		name      string
		path      string
		expErrMsg []string
	}{
		{"error status and not a json response", "/bad-status", []string{
			"http response status code not in success range: 400",
			ErrNotJSONResp.Error(),
		}},
		{"json response fails to unmarshal", "/bad-json", []string{
			"unexpected end of JSON input",
		}},
	}

	for _, tc := range tests {
		for _, retry := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/retry=%t", tc.name, retry), func(t *testing.T) {
				sr.Reset()

				var target map[string]any
				_, err := c.Do(context.Background(),
					ReqOpts().Path(tc.path),
					ReqOpts().Retry(retry),
					ReqOpts().UnmarshalJSONRespTo(&target),
				)
				require.Error(t, err)

				var do sdktrace.ReadOnlySpan
				for _, s := range sr.Ended() {
					if s.Name() == "xhttp.request.do" {
						do = s
					}
				}
				require.NotNil(t, do, "xhttp.request.do span must be ended")
				require.Equal(t, codes.Error, do.Status().Code)

				var recorded []string
				for _, ev := range do.Events() {
					for _, a := range ev.Attributes {
						if a.Key == "exception.message" {
							recorded = append(recorded, a.Value.AsString())
						}
					}
				}
				for _, msg := range tc.expErrMsg {
					require.Contains(t, recorded, msg, "the error must be recorded on the request span")
				}
			})
		}
	}
}
