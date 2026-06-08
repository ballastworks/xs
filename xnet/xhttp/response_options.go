package xhttp

import (
	"io"
	"net/http"
	"net/textproto"

	"github.com/ballastworks/xs/xlog/xslog"
)

// if you change this, make sure you also alter accordingly:
// - func (cfg respConfig) build() Response
type respConfig struct {
	factory *ResponseFactory
	responseBodyType
	statusCode int
	header     http.Header
	body       any

	withRespConfig withRespConfig
}

type RespOption func(*respConfig)

type respOpts struct{}

func RespOpts() respOpts {
	return respOpts{}
}

func (respOpts) Factory(rf *ResponseFactory) RespOption {
	return func(cfg *respConfig) {
		cfg.factory = rf
	}
}

func (respOpts) LoggerDisabled(b bool) RespOption {
	return func(cfg *respConfig) {
		cfg.withRespConfig.loggerDisabled = b
	}
}

func (respOpts) LoggerFactory(logf xslog.LoggerFactory) RespOption {
	return func(cfg *respConfig) {
		cfg.withRespConfig.logf = logf
	}
}

func (respOpts) JsonBody(v any) RespOption {
	return func(cfg *respConfig) {
		cfg.responseBodyType = responseBodyJson
		cfg.body = v
	}
}

func (respOpts) BytesBody(b []byte) RespOption {
	return func(cfg *respConfig) {
		cfg.responseBodyType = responseBodyBytes
		cfg.body = b
	}
}

func (respOpts) ReaderBody(r io.Reader) RespOption {
	return func(cfg *respConfig) {
		cfg.responseBodyType = responseBodyReader
		cfg.body = r
	}
}

// Header destructively sets the http header contents
//
// To add multiple sets of keys and values rather than completely overwrite the
// contents use SetHeaders()
func (respOpts) Header(h http.Header) RespOption {
	return func(cfg *respConfig) {
		// note, std sdk will handle a nil http.Header value properly
		cfg.header = h.Clone()
	}
}

// SetHeaders takes all key-value pairs from a header and replaces only
// the matching key-value pairs. If no match is found then the new key-value
// pair is added to the header set.
//
// To completely replace all pairs regardless of overlap, use Header()
func (respOpts) SetHeaders(h http.Header) RespOption {
	if len(h) == 0 {
		return func(*respConfig) {}
	}

	return func(cfg *respConfig) {
		if cfg.header == nil {
			cfg.header = http.Header{}
		}

		for k, v := range h.Clone() {
			cfg.header[k] = v
		}
	}
}

func (respOpts) HeaderValue(k, v string) RespOption {
	return func(cfg *respConfig) {
		if cfg.header == nil {
			cfg.header = http.Header{}
		}

		cfg.header.Set(k, v)
	}
}

func (respOpts) HeaderValues(k string, values ...string) RespOption {
	k = textproto.CanonicalMIMEHeaderKey(k)

	return func(cfg *respConfig) {

		v := make([]string, len(values))
		copy(v, values)

		if cfg.header == nil {
			cfg.header = http.Header{}
		}

		cfg.header[k] = v
	}
}

func (respOpts) AddHeaderValue(k, v string) RespOption {
	return func(cfg *respConfig) {

		if cfg.header == nil {
			cfg.header = http.Header{}
		}

		cfg.header.Add(k, v)
	}
}

func (respOpts) AddHeaderValues(k string, values ...string) RespOption {
	if len(values) == 0 {
		return func(*respConfig) {}
	}

	k = textproto.CanonicalMIMEHeaderKey(k)

	return func(cfg *respConfig) {

		if cfg.header == nil {
			cfg.header = http.Header{}
		}

		cfg.header[k] = append(cfg.header[k], values...)
	}
}

// StatusCode should be used to convey standard non-error responses and not to
// construct error messages or messages in general with error status codes. To
// construct error messages instead see NewErrResp and NewInternalErrResp.
//
// This function panics if the status code value is less than zero.
//
// If unspecified the response status code will be 200.
//
// Note that standard IANA range is really [100, 599] inclusive and behavior of
// clients when they receive values outside this range can be undefined.
func (respOpts) StatusCode(v int) RespOption {
	return func(cfg *respConfig) {
		cfg.statusCode = v
	}
}
