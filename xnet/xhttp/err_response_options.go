package xhttp

import (
	"net/http"
	"net/textproto"

	"github.com/ballastworks/xs/xlog/xslog"
)

type errRespConfig struct {
	factory       *ResponseFactory
	statusCode    int
	header        http.Header
	cause         error
	errCode       string
	devMsg        string
	cowDoneHeader bool

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

func (errRespOpts) Headers(h http.Header) ErrRespOption {
	return func(cfg *errRespConfig) {
		cfg.header = h
		cfg.cowDoneHeader = false
	}
}

func (errRespOpts) SetHeader(k, v string) ErrRespOption {
	return func(cfg *errRespConfig) {
		if cfg.header == nil {
			cfg.header = http.Header{}
		} else if !cfg.cowDoneHeader {
			cfg.header = cfg.header.Clone()
		}
		cfg.cowDoneHeader = true

		cfg.header.Set(k, v)
	}
}

func (errRespOpts) AddHeader(k, v string) ErrRespOption {
	return func(cfg *errRespConfig) {
		if cfg.header == nil {
			cfg.header = http.Header{}
		} else if !cfg.cowDoneHeader {
			cfg.header = cfg.header.Clone()
		}
		cfg.cowDoneHeader = true

		cfg.header.Add(k, v)
	}
}

func (errRespOpts) AddHeaders(k string, values ...string) ErrRespOption {

	k = textproto.CanonicalMIMEHeaderKey(k)

	return func(cfg *errRespConfig) {

		if len(values) == 0 {
			return
		}

		if cfg.header == nil {
			cfg.header = http.Header{}
		} else if !cfg.cowDoneHeader {
			cfg.header = cfg.header.Clone()
		}
		cfg.cowDoneHeader = true

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
