package xhttp

import (
	"context"
	"io"
	"net/http"
	"net/textproto"

	"github.com/ballastworks/xs/xlog/xslog"
)

type FluentResponse struct {
	factory *ResponseFactory
	respConfig
}

func (fr *FluentResponse) Factory(rf *ResponseFactory) *FluentResponse {
	fr.factory = rf
	return fr
}

func (fr *FluentResponse) LoggerDisabled(b bool) *FluentResponse {
	fr.withRespConfig.loggerDisabled = b
	return fr
}

func (fr *FluentResponse) LoggerFactory(logf xslog.LoggerFactory) *FluentResponse {
	fr.withRespConfig.logf = logf
	return fr
}

// StatusCode should be used to convey standard non-error responses and not to
// construct error messages or messages in general with error status codes. To
// construct error messages instead see NewErrResp.
//
// This function panics if the status code value is less than zero.
//
// If unspecified the response status code will be 200.
//
// Note that standard IANA range is really [100, 599] inclusive and behavior of
// clients when they receive values outside this range can be undefined.
func (fr *FluentResponse) StatusCode(v int) *FluentResponse {
	if v <= 0 {
		panic("invalid status code: must be > 0")
	}

	fr.statusCode = v
	return fr
}

func (fr *FluentResponse) JsonBody(v any) *FluentResponse {
	fr.responseBodyType = responseBodyJson
	fr.body = v
	return fr
}

func (fr *FluentResponse) BytesBody(b []byte) *FluentResponse {
	fr.responseBodyType = responseBodyBytes
	fr.body = b
	return fr
}

func (fr *FluentResponse) ReaderBody(r io.Reader) *FluentResponse {
	fr.responseBodyType = responseBodyReader
	fr.body = r
	return fr
}

func (fr *FluentResponse) Headers(h http.Header) *FluentResponse {
	// note, std sdk will handle a nil http.Header value properly
	fr.header = h.Clone()
	return fr
}

func (fr *FluentResponse) SetHeader(k, v string) *FluentResponse {
	if fr.header == nil {
		fr.header = http.Header{}
	}

	fr.header.Set(k, v)
	return fr
}

func (fr *FluentResponse) AddHeader(k, v string) *FluentResponse {
	if fr.header == nil {
		fr.header = http.Header{}
	}

	fr.header.Add(k, v)
	return fr
}

func (fr *FluentResponse) AddHeaders(k string, values ...string) *FluentResponse {
	k = textproto.CanonicalMIMEHeaderKey(k)

	if len(values) == 0 {
		return fr
	}

	if fr.header == nil {
		fr.header = http.Header{}
	}

	fr.header[k] = append(fr.header[k], values...)
	return fr
}

func (fr *FluentResponse) With(options ...WithRespOption) *FluentResponse {

	fr.withRespConfig = fr.withRespConfig.with(options...)

	return fr
}

func (fr *FluentResponse) getFactory() *ResponseFactory {
	if f := fr.factory; f != nil {
		return f
	}

	return DefaultResponseFactory()
}

func (fr *FluentResponse) response() Response {
	return Response{
		fr.getFactory(),
		nil,
		fr.responseBodyType,
		fr.statusCode,
		fr.header,
		fr.body,
		fr.withRespConfig,
	}
}

func (fr *FluentResponse) StaticHandler() http.Handler {
	return fr.response().StaticHandler()
}

// WriteResp writes the HTTP response to the given ResponseWriter
// using the context from the provided context.Context.
//
// This method constructs a Response object from the FluentResponse
// and delegates the actual writing of the response to the Response's
// WriteResp method.
//
// If the FluentResponse is reused across multiple requests and middlewares
// may modify pointer type contents within it, ensure that the FluentResponse
// is first deep cloned via a call to Clone() before invoking this method.
func (fr *FluentResponse) WriteResp(ctx context.Context, w http.ResponseWriter) {
	fr.response().WriteResp(ctx, w)
}

// ServeHTTP writes the HTTP response to the given ResponseWriter
// using the context from the provided context.Context.
//
// This method constructs a Response object from the FluentResponse
// and delegates the actual writing of the response to the Response's
// WriteResp method.
//
// If the FluentResponse is reused across multiple requests and middlewares
// may modify pointer type contents within it, ensure that the FluentResponse
// is first deep cloned via a call to Clone() before invoking this method.
func (fr *FluentResponse) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	fr.WriteResp(r.Context(), w)
}

func (fr *FluentResponse) Clone() *FluentResponse {
	dst := *fr

	if fr.header != nil {
		dst.header = fr.header.Clone()
	}

	return &dst
}
