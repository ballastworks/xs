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

func NewInternalErrResp(err error) ErrResponse {
	return (*ResponseFactory)(nil).NewInternalErr(err)
}

func NewErrResp(statusCode int, errCode, devMsg string) ErrResponse {
	return (*ResponseFactory)(nil).NewErr(statusCode, errCode, devMsg)
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

func (rf *ResponseFactory) NewInternalErr(err error) ErrResponse {
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

	return ErrResponse{
		rf,
		err,
		http.StatusInternalServerError,
		"internal-server-error",
		"Internal Server Error",
		withErrRespConfig{},
	}
}

func (rf *ResponseFactory) NewErr(statusCode int, errCode, devMsg string) ErrResponse {
	return ErrResponse{
		rf,
		nil,
		statusCode,
		errCode,
		devMsg,
		withErrRespConfig{},
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
