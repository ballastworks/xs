package xhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"

	"github.com/ballastworks/xs/internal/xpatterns"
	"github.com/ballastworks/xs/xcontext"
	"github.com/ballastworks/xs/xcontext/xspan"
	"github.com/ballastworks/xs/xerrors"
	"github.com/ballastworks/xs/xlog/xslog"
)

var (
	errStaticHandlerUnavailable = NewErrResp(
		ErrRespOpts().StatusCode(http.StatusServiceUnavailable),
		ErrRespOpts().ErrCode("service-unavailable"),
		ErrRespOpts().DevMsg("static handler detected context deadline exceeded likely due to some internal response timeout policy"),
		ErrRespOpts().FailSpan(true),
	)
)

type ErrResponse struct {
	factory    *ResponseFactory
	cause      error
	statusCode int
	errCode    string
	devMsg     string
	header     http.Header

	withErrRespConfig
}

type errResponder interface {
	xpatterns.ErrResponder[ErrResponse]
	http.Handler
}

func (er ErrResponse) Error() string {
	const (
		prefix      = "ErrResponse: "
		sep         = " - "
		causePrefix = ": "
	)

	statusStr := strconv.Itoa(er.getStatusCode())
	causeStr := ""
	if er.cause != nil {
		causeStr = er.cause.Error()
	}

	// Precompute final length
	length := len(prefix) +
		len(statusStr) +
		len(sep) +
		len(er.errCode)

	if er.devMsg != "" {
		length += len(sep) + len(er.devMsg)
	}

	if causeStr != "" {
		length += len(causePrefix) + len(causeStr)
	}

	var b strings.Builder
	b.Grow(length)

	b.WriteString(prefix)
	b.WriteString(statusStr)
	b.WriteString(sep)
	b.WriteString(er.errCode)

	if er.devMsg != "" {
		b.WriteString(sep)
		b.WriteString(er.devMsg)
	}

	if causeStr != "" {
		b.WriteString(causePrefix)
		b.WriteString(causeStr)
	}

	return b.String()
}

func (er ErrResponse) Unwrap() error {
	return er.cause
}

func (er ErrResponse) Body() any {
	return er.body()
}

func (er ErrResponse) StatusCode() int {
	return er.getStatusCode()
}

func (er ErrResponse) ErrCode() string {
	return er.errCode
}

func (er ErrResponse) DevMsg() string {
	return er.devMsg
}

var errUnknownCause = errors.New("unknown cause")

func (er ErrResponse) CausedBy(ctx context.Context, err error) ErrResponse {

	// TODO: it's likely this function should be refactored into
	// a logging call
	//
	// We only keep it around for the rendering .Error()
	// behavior of the ErrResponse type.
	//
	// This way the stack aspect becomes a conveyance at the
	// time this function is called which is the fallback
	// of the current implementation behavior anyways.
	//
	// In fact, gonna refactor the function signature to
	// require a context as this should only ever be called
	// at execution time and not init.

	if v, ok := err.(errResponder); ok {
		if e := v.Unwrap(); e != nil {
			err = e
		} else {
			err = xerrors.WithStack(errUnknownCause)
		}
	} else if te, ok := err.(interface{ Unwrap() error }); ok {
		unwrappedErr := te.Unwrap()
		if v, ok := unwrappedErr.(errResponder); ok {
			if e := v.Unwrap(); e != nil {
				err = e
			} else {
				err = xerrors.WithStack(errUnknownCause)
			}
		}
	} else {
		err = xerrors.WithStack(err)
	}

	er.cause = err

	return er
}

func (er ErrResponse) getStatusCode() int {

	if sc := er.statusCode; sc >= 400 {
		return sc
	}

	return http.StatusInternalServerError
}

func (er ErrResponse) body() any {
	return struct {
		ErrCode string `json:"errcode"`
	}{er.errCode}
}

func (er ErrResponse) Resp() Response {
	// TODO: see if allocations can be reduced by using a pointer receiver instead of making a copy
	// verify that such a change does not introduce side effects

	header := er.header
	if header == nil {
		header = http.Header{}
	}

	return Response{
		er.factory,
		er,
		responseBodyJson,
		er.getStatusCode(),
		header,
		er.body(),
		er.withRespConfig,
	}
}

func (er ErrResponse) With(options ...WithErrRespOption) ErrResponse {

	er.withErrRespConfig = er.withErrRespConfig.with(options...)

	return er
}

func (er ErrResponse) WriteResp(ctx context.Context, w http.ResponseWriter) {
	if er.withErrRespConfig.failSpan {
		xspan.Fail(ctx, er.cause, er.Error())
	}

	er.Resp().WriteResp(ctx, w)
}

func (er ErrResponse) StaticHandler() http.Handler {
	return er.Resp().StaticHandler()
}

func (er ErrResponse) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	er.WriteResp(r.Context(), w)
}

type responseBodyType uint8

const (
	responseBodyNone responseBodyType = iota
	responseBodyJson
	responseBodyReader
	responseBodyBytes
)

type Response struct {
	factory          *ResponseFactory
	errResp          error
	responseBodyType responseBodyType
	statusCode       int
	header           http.Header
	body             any

	withRespConfig
}

func (resp Response) getFactory() *ResponseFactory {
	if f := resp.factory; f != nil {
		return f
	}

	return DefaultResponseFactory()
}

func (resp Response) logger(ctx context.Context) xslog.Logger {

	logf := resp.loggerFactory()
	return logf.Logger(ctx)
}

// // TODO: also have ability to set option on response factory to act as a "default" if it does not have a lot of atomic overhead
// type staticHandlerConfig struct{}
// type StaticHandlerOption func(*staticHandlerConfig)
// type staticHandlerOpts struct{}
// func StaticHandlerOpts() staticHandlerOpts {
// 	return staticHandlerOpts{}
// }

// StaticHandler will panic if there are any problems rendering the response body
//
// Use this function to create a frequently used static response handler
// that does not change between requests.
//
// The returned handler will write the same response every time it is called.
//
// Note that if the response represents an error (either via ErrResponse
// or via status code in the error range) then any logging function
// configured on the ResponseFactory will still be called each time
// the handler is invoked.
//
// Note that if you want to detect issues with the client such as the client
// disconnects, or the client fails to receive the full response or have
// additional behavior such as detecting that the response was far too slow
// then implement that in a middleware that wraps the result of this function.
func (resp Response) StaticHandler() http.Handler {
	w := httptest.NewRecorder() // TODO: pool?

	// suppress log messages while computing the handler response
	{
		resp := resp.With(WithRespOpts().LoggerDisabled(true))
		resp.WriteResp(context.Background(), w)
	}

	res := w.Result()

	var buf bytes.Buffer

	_, err := io.Copy(&buf, res.Body)
	if err != nil {
		panic(err)
	}

	header := res.Header
	// header.Del("Date") // not needed, this gets added automatically in some higher layer

	sc := res.StatusCode

	body := buf.Bytes()

	// make sure the body kept around in memory is not wasteful
	if buf.Cap() > len(body) {
		newBody := make([]byte, len(body))
		copy(newBody, body)
		body = newBody
	}

	// Note that the write observer middleware will do all the heavy lifting of
	// truly handling a client disconnect. Do not implement handling those
	// concerns here at this level.

	var handler func(w http.ResponseWriter, r *http.Request)
	if len(header) > 0 {
		handler = func(w http.ResponseWriter, r *http.Request) {

			h := w.Header()

			// TODO: add a config option that disables copying headers each time
			// if the user knows the headers will not change between requests
			//
			// this would improve performance in high throughput scenarios
			//
			// but would be a foot-gun if the user later modifies the headers
			// expecting them to be different between requests

			header := header.Clone()

			for k, values := range header {
				h[k] = values
			}

			// check for a policy that killed the context which means we should
			// instead render a 503 to a still connected client
			{
				ctx := r.Context()

				if err := xcontext.Cause(ctx); err != nil && middlewareShowsClientConnected(ctx) {
					// context errored likely due to an internal response timeout/interrupt policy

					// TODO: test what happens if http after-read compute and
					// reply timeout is set and reached

					// undo response header changes
					for k := range header {
						delete(h, k)
					}

					errStaticHandlerUnavailable.CausedBy(ctx, err).WriteResp(ctx, w)
					return
				}
			}

			w.WriteHeader(sc)

			_, ignoredErr := w.Write(body)
			_ = ignoredErr
			// ignore the error at this level only because the write observer
			// middleware will intercept, log, and even fail + record the error
			// in the span
		}
	} else {
		handler = func(w http.ResponseWriter, r *http.Request) {

			// check for a policy that killed the context which means we should
			// instead render a 503 to a still connected client
			{
				ctx := r.Context()

				if err := xcontext.Cause(ctx); err != nil && middlewareShowsClientConnected(ctx) {
					// context errored likely due to an internal response timeout/interrupt policy

					// TODO: test what happens if http after-read compute and
					// reply timeout is set and reached

					errStaticHandlerUnavailable.CausedBy(ctx, err).WriteResp(ctx, w)
					return
				}
			}

			w.WriteHeader(sc)

			_, ignoredErr := w.Write(body)
			_ = ignoredErr
			// ignore the error at this level only because the write observer
			// middleware will intercept, log, and even fail + record the error
			// in the span
		}
	}

	// wrap handler so that errors are logged
	if !resp.loggerDisabled && (resp.errResp != nil || statusCodeInErrorRange(sc)) {
		if rf := resp.factory; rf != nil {

			// At static response construction time the response factory was
			// well known, so keep this knowledge and log in the fashion
			// the response factory indicates we should in this context before
			// the final render is issued to the request originator.

			if logFunc := rf.errRespLoggingFunc; logFunc != nil {
				next := handler
				handler = func(w http.ResponseWriter, r *http.Request) {

					ctx := r.Context()
					logger := rf.Logger(ctx)
					logFunc(ctx, logger, resp.errResp, sc)

					next(w, r)
				}
			}
		} else {

			// At static response construction time the response factory was
			// NOT well known, so the handler must resolve this dynamically
			// each handler invocation so we can log in the fashion the current
			// default response factory instance indicates we should in before
			// the final render is issued to the request originator.

			next := handler
			handler = func(w http.ResponseWriter, r *http.Request) {
				rf := DefaultResponseFactory()

				if logFunc := rf.errRespLoggingFunc; logFunc != nil {

					ctx := r.Context()
					logger := rf.Logger(ctx)
					logFunc(ctx, logger, resp.errResp, sc)
				}

				next(w, r)
			}
		}
	}

	return http.HandlerFunc(handler)
}

func (resp Response) With(options ...WithRespOption) Response {

	resp.withRespConfig = resp.withRespConfig.with(options...)

	return resp
}

// WriteResp writes the HTTP response to the given ResponseWriter
// using the context from the provided context.Context.
//
// If the Response is reused across multiple requests and middlewares
// may modify pointer type contents within it, ensure that the Response
// is first deep cloned via a call to Clone() before invoking this method.
func (resp Response) WriteResp(ctx context.Context, w http.ResponseWriter) {

	sc := resp.statusCode

	if sc == 0 {
		sc = http.StatusOK
	}

	if resp.errResp != nil || statusCodeInErrorRange(sc) {
		f := resp.getFactory()
		if logFunc := f.errRespLoggingFunc; logFunc != nil {
			logger := resp.logger(ctx)
			logFunc(ctx, logger, resp.errResp, sc)
		}

		if err := resp.errResp; err != nil {
			if v := xerrors.StacktraceReleaser(err); v != nil {
				v.ReleaseStacktrace()
			}
		}
	}

	switch resp.responseBodyType {
	case responseBodyNone:

		header := w.Header()

		for k, v := range resp.header {
			header[k] = v
		}

		w.WriteHeader(sc)
	case responseBodyJson:

		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(resp.body); err != nil {

			slog.DebugContext(ctx,
				"failed to json encode response body",
				"error", err,
			)

			resp.getFactory().
				NewInternalErr(fmt.Errorf("failed to json encode response body: %w", err)).
				WriteResp(ctx, w)

			return
		}

		header := w.Header()

		for k, v := range resp.header {
			header[k] = v
		}

		header.Set("Content-Type", "application/json")
		header.Set("Content-Length", strconv.Itoa(buf.Len()))

		w.WriteHeader(sc)

		if _, err := io.Copy(w, &buf); err != nil {
			// ignore the error, but at least record the error and mark the span as failed
			xspan.Fail(ctx, err, "")
		}
	case responseBodyReader:
		var reader io.Reader

		if v, ok := resp.body.(io.Reader); ok {
			reader = v
		} else {
			err := fmt.Errorf("body content not io.Reader: %T", resp.body)

			slog.DebugContext(ctx,
				"body content not io.Reader",
				"body_type", fmt.Sprintf("%T", resp.body),
			)

			resp.getFactory().
				NewInternalErr(err).
				WriteResp(ctx, w)

			return
		}

		header := w.Header()

		for k, v := range resp.header {
			header[k] = v
		}

		w.WriteHeader(sc)

		if _, err := io.Copy(w, reader); err != nil {
			// ignore the error, but at least record the error and mark the span as failed
			xspan.Fail(ctx, err, "")
		}
	case responseBodyBytes:
		var content []byte

		if v, ok := resp.body.([]byte); ok {
			content = v
		} else {
			err := fmt.Errorf("body content not []byte: %T", resp.body)

			slog.DebugContext(ctx,
				"body content not []byte",
				"body_type", fmt.Sprintf("%T", resp.body),
			)

			resp.getFactory().
				NewInternalErr(err).
				WriteResp(ctx, w)

			return
		}

		header := w.Header()

		for k, v := range resp.header {
			header[k] = v
		}

		header.Set("Content-Length", strconv.Itoa(len(content)))

		w.WriteHeader(sc)

		if _, err := io.Copy(w, bytes.NewReader(content)); err != nil {
			// ignore the error, but at least record the error and mark the span as failed
			xspan.Fail(ctx, err, "")
		}
	}
}

// ServeHTTP writes the HTTP response to the given ResponseWriter
// using the context from the provided context.Context.
//
// This method delegates the actual writing of the response to the
// Response's WriteResp method.
//
// If the Response is reused across multiple requests and middlewares
// may modify pointer type contents within it, ensure that the Response
// is first deep cloned via a call to Clone() before invoking this method.
func (resp Response) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	resp.WriteResp(r.Context(), w)
}

func (resp Response) Clone() Response {
	dst := resp

	if resp.header != nil {
		dst.header = resp.header.Clone()
	}

	return dst
}

//
// helpers
//

func statusCodeInErrorRange(sc int) bool {
	return sc < 100 || sc >= 400
}

func middlewareShowsClientConnected(ctx context.Context) bool {
	cdo := clientDisconnectObserverFromContext(ctx)
	if cdo == nil {
		return false
	}

	return cdo.Connected()
}
