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

func (respOpts) Headers(h http.Header) RespOption {
	return func(cfg *respConfig) {
		// note, std sdk will handle a nil http.Header value properly
		cfg.header = h.Clone()
	}
}

func (respOpts) SetHeader(k, v string) RespOption {
	return func(cfg *respConfig) {
		if cfg.header == nil {
			cfg.header = http.Header{}
		}

		cfg.header.Set(k, v)
	}
}

func (respOpts) AddHeader(k, v string) RespOption {
	return func(cfg *respConfig) {

		if cfg.header == nil {
			cfg.header = http.Header{}
		}

		cfg.header.Add(k, v)
	}
}

func (respOpts) AddHeaders(k string, values ...string) RespOption {

	k = textproto.CanonicalMIMEHeaderKey(k)

	return func(cfg *respConfig) {

		if len(values) == 0 {
			return
		}

		if cfg.header == nil {
			cfg.header = http.Header{}
		}

		cfg.header[k] = append(cfg.header[k], values...)
	}
}
