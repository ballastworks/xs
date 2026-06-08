package xhttp

import (
	"net/http"
	"net/textproto"

	"github.com/ballastworks/xs/xlog/xslog"
)

type errRespConfig struct {
	factory    *ResponseFactory
	statusCode int
	header     http.Header
	cause      error
	errCode    string
	devMsg     string

	withErrRespConfig withErrRespConfig
}

type ErrRespOption func(*errRespConfig)

type errRespOpts struct{}

func ErrRespOpts() errRespOpts {
	return errRespOpts{}
}

func (errRespOpts) Factory(rf *ResponseFactory) ErrRespOption {
	return func(cfg *errRespConfig) {
		cfg.factory = rf
	}
}

func (errRespOpts) LoggerDisabled(b bool) ErrRespOption {
	return func(cfg *errRespConfig) {
		cfg.withErrRespConfig.loggerDisabled = b
	}
}

func (errRespOpts) LoggerFactory(logf xslog.LoggerFactory) ErrRespOption {
	return func(cfg *errRespConfig) {
		cfg.withErrRespConfig.logf = logf
	}
}

// Header destructively sets the http header contents
//
// To add multiple sets of keys and values rather than completely overwrite the
// contents use SetHeaders()
func (errRespOpts) Header(h http.Header) ErrRespOption {
	return func(cfg *errRespConfig) {
		cfg.header = h.Clone()
	}
}

// SetHeaders takes all key-value pairs from a header and replaces only
// the matching key-value pairs. If no match is found then the new key-value
// pair is added to the header set.
//
// To completely replace all pairs regardless of overlap, use Header()
func (errRespOpts) SetHeaders(h http.Header) ErrRespOption {
	if len(h) == 0 {
		return func(*errRespConfig) {}
	}

	return func(cfg *errRespConfig) {
		if cfg.header == nil {
			cfg.header = http.Header{}
		}

		for k, v := range h.Clone() {
			cfg.header[k] = v
		}
	}
}

func (errRespOpts) HeaderValue(k, v string) ErrRespOption {
	return func(cfg *errRespConfig) {
		if cfg.header == nil {
			cfg.header = http.Header{}
		}

		cfg.header.Set(k, v)
	}
}

func (errRespOpts) HeaderValues(k string, values ...string) ErrRespOption {
	k = textproto.CanonicalMIMEHeaderKey(k)

	return func(cfg *errRespConfig) {
		if cfg.header == nil {
			cfg.header = http.Header{}
		}

		v := make([]string, len(values))
		copy(v, values)

		cfg.header[k] = v
	}
}

func (errRespOpts) AddHeaderValue(k, v string) ErrRespOption {
	return func(cfg *errRespConfig) {
		if cfg.header == nil {
			cfg.header = http.Header{}
		}

		cfg.header.Add(k, v)
	}
}

func (errRespOpts) AddHeaderValues(k string, values ...string) ErrRespOption {
	if len(values) == 0 {
		return func(*errRespConfig) {}
	}

	k = textproto.CanonicalMIMEHeaderKey(k)

	return func(cfg *errRespConfig) {
		if cfg.header == nil {
			cfg.header = http.Header{}
		}

		cfg.header[k] = append(cfg.header[k], values...)
	}
}

func (errRespOpts) StatusCode(v int) ErrRespOption {
	return func(cfg *errRespConfig) {
		cfg.statusCode = v
	}
}

func (errRespOpts) Cause(v error) ErrRespOption {
	return func(cfg *errRespConfig) {
		cfg.cause = v
	}
}

func (errRespOpts) ErrCode(s string) ErrRespOption {
	return func(cfg *errRespConfig) {
		cfg.errCode = s
	}
}

func (errRespOpts) DevMsg(s string) ErrRespOption {
	return func(cfg *errRespConfig) {
		cfg.devMsg = s
	}
}

func (errRespOpts) FailSpan(b bool) ErrRespOption {
	return func(cfg *errRespConfig) {
		cfg.withErrRespConfig.failSpan = b
	}
}
