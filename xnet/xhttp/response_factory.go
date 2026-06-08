package xhttp

import (
	"errors"
	"net/http"

	"github.com/ballastworks/xs/xerrors"
	"github.com/ballastworks/xs/xlog/xslog"
)

var (
	ErrBadResponseFactoryConfig = errors.New("bad response factory config")
)

// nil factory exports:

func NewInternalErrResp(cause error) ErrResponse {
	return (*ResponseFactory)(nil).NewInternalErr(cause)
}

func NewErrResp(options ...ErrRespOption) ErrResponse {
	return (*ResponseFactory)(nil).NewErr(options...)
}

func NewResp(options ...RespOption) Response {
	return (*ResponseFactory)(nil).NewResp(options...)
}

func NewFluentResp() *FluentResponse {
	return (*ResponseFactory)(nil).NewFluentResp()
}

// non-nil factory exports:

type ResponseFactory struct {
	xslog.LoggerFactory
	errRespLoggingFunc ErrRespLoggingFunc
}

func NewResponseFactory(options ...ResponseFactoryOption) (*ResponseFactory, error) {

	cfg := responseFactoryConfig{
		errRespLoggingFunc: DefaultErrRespLoggingFunc,
	}

	for _, f := range options {
		f(&cfg)
	}

	if err := cfg.validate(); err != nil {
		return nil, errors.Join(ErrBadResponseFactoryConfig, err)
	}

	return &ResponseFactory{cfg.logf, cfg.errRespLoggingFunc}, nil
}

func (rf *ResponseFactory) NewInternalErr(cause error) ErrResponse {
	err := cause
	if v, ok := err.(ErrResponse); ok {
		return v
	} else if te, ok := err.(interface{ Unwrap() error }); ok {
		innerErr := te.Unwrap()
		if v, ok := innerErr.(ErrResponse); ok {
			return v
		}
	} else {
		err = xerrors.WithStack(err)
	}

	er := rf.NewErr()
	er.cause = err
	return er
}

func (rf *ResponseFactory) NewErr(options ...ErrRespOption) ErrResponse {

	cfg := errRespConfig{
		factory:    rf,
		statusCode: http.StatusInternalServerError,
	}

	for _, f := range options {
		f(&cfg)
	}

	if cfg.statusCode == http.StatusInternalServerError {

		if cfg.errCode == "" {
			cfg.errCode = "internal-server-error"
		}

		if cfg.devMsg == "" {
			cfg.devMsg = "Internal Server Error"
		}
	}

	return ErrResponse{
		cfg.factory,
		cfg.cause,
		cfg.statusCode,
		cfg.errCode,
		cfg.devMsg,
		cfg.header,
		cfg.withErrRespConfig,
	}
}

func (rf *ResponseFactory) NewResp(options ...RespOption) Response {

	cfg := respConfig{
		factory: rf,
	}

	for _, f := range options {
		f(&cfg)
	}

	return Response{
		cfg.factory,
		nil,
		cfg.responseBodyType,
		cfg.statusCode,
		cfg.header,
		cfg.body,
		cfg.withRespConfig,
	}
}

func (rf *ResponseFactory) NewFluentResp() *FluentResponse {
	return &FluentResponse{rf, respConfig{}}
}
