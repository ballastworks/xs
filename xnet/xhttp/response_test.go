package xhttp

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/ballastworks/xs/xlog/xslog"
	"github.com/josephcopenhaver/tbdd-go"
)

func TestRouteNotFoundHandler(t *testing.T) {
	type TC struct {
		handler http.Handler
		req     *http.Request
	}
	type R struct {
		statusCode int
		header     http.Header
		body       string
		bodyErr    error
	}

	tc := func() TC {
		//
		// Setup Router
		//

		rt, err := NewRouter(
			RouterOpts().LoggerFactory(xslog.NopFactory()),
			RouterOpts().IgnoreTrailingSlash(true),
		)
		if err != nil {
			panic(err)
		}

		//
		// Setup Server
		//

		srv, err := NewServer(
			SrvOpts().Server(&http.Server{
				Addr:    "127.0.0.1:0",
				Handler: rt,
			}),
			SrvOpts().LoggerFactory(xslog.NopFactory()),
		)
		if err != nil {
			panic(err)
		}

		req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:0/404.json", http.NoBody)
		if err != nil {
			panic(err)
		}

		return TC{
			handler: srv,
			req:     req,
		}
	}()

	tbdd.WT(tc,
		// when
		"route not found handler is invoked",
		func(t *testing.T, tc TC) R {
			w := httptest.NewRecorder()

			tc.handler.ServeHTTP(w, tc.req)
			resp := w.Result()
			defer resp.Body.Close()

			b, err := io.ReadAll(resp.Body)
			return R{
				header:     resp.Header,
				statusCode: resp.StatusCode,
				body:       string(b),
				bodyErr:    err,
			}
		},
		// then
		"should 404",
		func(t *testing.T, tc TC, r R) {
			if err := r.bodyErr; err != nil {
				t.Fatalf("failed to read response body: %s", err.Error())
			}

			if r.statusCode != http.StatusNotFound {
				t.Fatal("received unexpected status code: " + strconv.Itoa(r.statusCode))
			}

			const expJSON = `{"errcode":"not-found"}`

			if strings.TrimRight(r.body, "\n") != expJSON {
				t.Fatal("received unexpected response body: " + r.body)
			}

			if len(r.header) != 2 {
				t.Fatal("too many headers in response, expected 2")
			}

			if r.header[`Content-Type`][0] != `application/json` {
				t.Fatal("unexpected Content-Type")
			}

			var lenOffset int
			if expJSON != r.body {
				lenOffset = 1
			}
			if r.header[`Content-Length`][0] != strconv.Itoa(len(expJSON)+lenOffset) {
				t.Fatal("unexpected Content-Length")
			}
		},
	).Run(t)
}
