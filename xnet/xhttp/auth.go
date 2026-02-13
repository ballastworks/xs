package xhttp

import (
	"context"
	"errors"
	"net/http"
)

var (
	ErrInvalidAuthAdderStrat = errors.New("invalid strategy: no auth adder strategy specified")
)

// AuthRefresher describes the behavior required to analyze a http round trip error response to determine if authentication details need to be refresh and to perform that refresh in some external data storage implementation.
type authRefresher interface {
	// IsAuthRejectedHttpResp should use the response and error (which may be an instance of ErrRespStatusCodeNotSuccess) to
	// conclude if the response is due to the resource gateway suddenly rejecting the authorization of the request
	CheckIsAuthRejectedHttpResp(*http.Response, error) bool

	// RefreshHttpAuth updates authentication state variables
	// utilized by a AuthAdder implementation.
	//
	// Implementations must be safe for concurrent use.
	RefreshHttpAuth(context.Context) error
}

// AuthAdder is used to add authentication details to a http request and return the new *http.Request
// to be used in a request.
//
// Special care must be taken to consider the lifecycle of attributes that are changed that are sourced from the input
// *http.Request argument and preserve idempotent behavior for changes made to those shared attributes should they be
// already modified by a prior invocation of this implementation or deep-clone the input argument via req.Clone(req.Context())
// to ensure idempotent behavior in all cases.
//
// The caller assumes NO responsibility in ensuring that this function does not have side effects
// (it does not call .Clone() before invoking and the context set in the request is the same context used when Do is called on the http client)
//
// This function must preserve the context or maintain a full parent-child context relationship to the context within the input.
//
// When implementing an AuthAdder, consider adding the AuthAdderStrategyDescriber interface which informs the caller that authentication details will or will not change over time.
//
// Also if you need to suppress authentication refreshing behavior implement the AuthAdderSuppressor interface to do so.
type AuthAdder interface {
	// AuthAdder is exported because it is used as an argument type for the clientOpts.AuthAdder option.

	// AddAuthToHttpRequest is used to add authentication details to a http request and return the new *http.Request
	// to be used in a request.
	//
	// Special care must be taken to consider the lifecycle of attributes that are changed that are sourced from the input
	// *http.Request argument and preserve idempotent behavior for changes made to those shared attributes should they be
	// already modified by a prior invocation of this implementation or deep-clone the input argument via req.Clone(req.Context())
	// to ensure idempotent behavior in all cases.
	//
	// The caller assumes NO responsibility in ensuring that this function does not have side effects
	// (it does not call .Clone() before invoking and the context set in the request is the same context used when Do is called on the http client)
	//
	// This function must preserve the context or maintain a full parent-child context relationship to the context within the input.
	AddAuthToHttpRequest(*http.Request) *http.Request

	authRefresher
}

type AuthAdderSuppressor interface {
	// AuthAdderSuppressor is exported because the basis in which it is useful is also exported: AuthAdder.

	IsHttpReqAuthRefresherEnabled() bool
}

type AuthAdderStrategyDescriber interface {
	// AuthAdderStrategyDescriber is exported because the basis in which it is useful is also exported: AuthAdder.

	// IsAddAuthToHttpRequestStrategyStatic will tell the caller to
	// ignore any other dynamic authentication resolution details and
	// compute the authentication configured request once and only once
	// per execution attempt even if retries are enabled.
	IsAddAuthToHttpRequestStrategyStatic() bool
}

// authMediator is a potentially concurrency-unsafe implementation of a ContextWrapper
//
// safety depends on the implementation of the authFunc value
type authMediator struct {
	wrapRequest      func(*http.Request) *http.Request
	isStrategyStatic bool
}

func newAuthMediator(wrapRequest func(*http.Request) *http.Request, isStrategyStatic bool) *authMediator {
	return &authMediator{wrapRequest, isStrategyStatic}
}

func (am *authMediator) AddAuthToHttpRequest(r *http.Request) *http.Request {
	return am.wrapRequest(r)
}

func (am *authMediator) IsAddAuthToHttpRequestStrategyStatic() bool {
	return am.isStrategyStatic
}

// CheckIsAuthRejectedHttpResp is a dummy implementation and ideally should never be called
func (am *authMediator) CheckIsAuthRejectedHttpResp(_ *http.Response, _ error) bool {
	return false
}

// RefreshHttpAuth is a dummy implementation and ideally should never be called
func (am *authMediator) RefreshHttpAuth(_ context.Context) error {
	return nil
}

// IsHttpReqAuthRefresherEnabled is a way to suppress the AuthRefresher implementation, but it should generally not be used
// outside of oddballs like the internal authMediator type.
func (am *authMediator) IsHttpReqAuthRefresherEnabled() bool {
	return false
}

type authAdderConfig struct {
	wrapRequest      func(*http.Request) *http.Request
	isStrategyStatic bool
}

type AuthAdderOption func(*authAdderConfig)
type authAdderOpts struct{}

func AuthAdderOpts() authAdderOpts {
	return authAdderOpts{}
}

type reqWrapper interface {
	WrapRequest(*http.Request) *http.Request
}

// StratAuthRequestWrapper is used to add authentication details to a http request and return the new *http.Request
// to be used in a request.
//
// Because it does not implement the AuthRefresher interface and state management concerns such as Rejected Authorization
// Checking, using this option is not recommended. It only exists as a courtesy for others to
// utilize in scenarios where authentication must be refreshed and maintained externally and singularly
// in an concurrent routine and cannot be initialized in a request execution context reliably. If a user
// finds that they need to use this strategy they are encouraged to open a feature request and describe their
// scenario in detail. Please instead write a custom AuthRefresher implementation if comfortable doing so.
//
// Callers should consider implementing interface{IsRequestWrapperStrategyStatic() bool}
// for the RequestWrapper that they pass in if the strategy for adding authorization does not change over
// time.
//
// Special care must be taken to consider the lifecycle of attributes that are changed that are sourced from the input
// *http.Request argument and preserve idempotent behavior for changes made to those shared attributes should they be
// already modified by a prior invocation of this implementation or deep-clone the input argument via req.Clone(req.Context())
// to ensure idempotent behavior in all cases.
//
// The caller assumes NO responsibility in ensuring that this function does not have side effects
// (it does not call .Clone() before invoking and the context set in the request is the same context used when Do is called on the http client)
//
// This function must preserve the context or maintain a full parent-child context relationship to the context within the input.
func (authAdderOpts) StratAuthRequestWrapper(rw reqWrapper) AuthAdderOption {
	return func(cfg *authAdderConfig) {
		cfg.wrapRequest = rw.WrapRequest

		var isStrategyStatic bool
		if v, ok := rw.(interface{ IsRequestWrapperStrategyStatic() bool }); ok {
			isStrategyStatic = v.IsRequestWrapperStrategyStatic()
		}
		cfg.isStrategyStatic = isStrategyStatic
	}
}

func (authAdderOpts) StratAuthorizationHeaderFunc(f func() string) AuthAdderOption {
	if f == nil {
		return func(cfg *authAdderConfig) {
			cfg.wrapRequest = nil
			cfg.isStrategyStatic = false
		}
	}
	return func(cfg *authAdderConfig) {
		cfg.wrapRequest = func(r *http.Request) *http.Request {
			secret := f()
			setAuthorizationHeader(r, secret)
			return r
		}
		cfg.isStrategyStatic = false
	}
}

func (authAdderOpts) StratAuthorizationHeaderVal(authorizationHeaderVal string) AuthAdderOption {
	return func(cfg *authAdderConfig) {
		cfg.wrapRequest = func(r *http.Request) *http.Request {
			setAuthorizationHeader(r, authorizationHeaderVal)
			return r
		}
		cfg.isStrategyStatic = true
	}
}

func setAuthorizationHeader(r *http.Request, authorizationHeaderVal string) {
	if r == nil {
		panic("nil http request")
	}

	if r.Header == nil {
		r.Header = http.Header{}
	}

	r.Header["Authorization"] = []string{authorizationHeaderVal}
}

func (cfg *authAdderConfig) validate() error {

	if cfg.wrapRequest == nil {
		return ErrInvalidAuthAdderStrat
	}

	return nil
}

// NewAuthAdder should ideally be called only during startup phase of applications
// and prebuilt implementation strategies should be reused per request and/or client
func NewAuthAdder(options ...AuthAdderOption) (AuthAdder, error) {

	cfg := authAdderConfig{}

	for _, f := range options {
		f(&cfg)
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return newAuthMediator(cfg.wrapRequest, cfg.isStrategyStatic), nil
}
