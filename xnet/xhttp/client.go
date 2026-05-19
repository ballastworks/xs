package xhttp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ballastworks/xs/xio"
	"github.com/ballastworks/xs/xlog/xslog"
	"go.opentelemetry.io/contrib/instrumentation/net/http/httptrace/otelhttptrace"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"golang.org/x/net/http/httpguts"
)

type renderType uint8

const (
	defaultRenderType renderType = iota
	readerRenderType
	jsonRenderType
)

const (
	headerKeyContentType              = "Content-Type"
	headerValueContentTypeJson        = "application/json"
	headerKeyUserAgent                = "User-Agent"
	msgErrBadGetBodyResp              = "bad GetBody response"
	prefixErrBadGetBodyResp           = msgErrBadGetBodyResp + ": "
	msgErrRespStatusCodeNotSuccess    = "http response status code not in success range"
	prefixErrRespStatusCodeNotSuccess = msgErrRespStatusCodeNotSuccess + ": "
	msgErrRetryDeadlineExceeded       = "retry deadline exceeded"
	prefixErrRetryDeadlineExceeded    = msgErrRetryDeadlineExceeded + ": "
)

var (
	ErrPerCallTimeoutMustBeGTZero       = errors.New("perCallTimeout must be greater than zero")
	ErrBadMethod                        = errors.New("invalid method")
	ErrNotJSONResp                      = errors.New("not a json response")
	ErrNoBodyInJSONResp                 = errors.New("no body in json response")
	ErrBadConfigReaderBodyOverrides     = errors.New("config would override calls that set body to io.Readers and lead to undefined behavior")
	ErrRespStatusCodeNotSuccess         = errors.New(msgErrRespStatusCodeNotSuccess)
	ErrNilUnmarshalTarget               = errors.New("nil unmarshal target")
	ErrNilGetBodyNotAllowedWhenRetrying = errors.New("nil GetBody value cannot be specified when retrying")
	ErrRetryDeadlineExceeded            = errors.New(msgErrRetryDeadlineExceeded)
	ErrBadGetBodyResp                   = errors.New(msgErrBadGetBodyResp)
	ErrPanicInCopy                      = errors.New("panic in io.Copy")
)

type errBadGetBodyResp struct {
	err error
}

func (e *errBadGetBodyResp) Error() string {
	return prefixErrBadGetBodyResp + e.err.Error()
}

func (e *errBadGetBodyResp) Is(target error) bool {
	return target == ErrBadGetBodyResp || errors.Is(e.err, target)
}

type errRespStatusCodeNotSuccess struct {
	statusCode int
}

func (e *errRespStatusCodeNotSuccess) Error() string {
	return prefixErrRespStatusCodeNotSuccess + strconv.Itoa(e.statusCode)
}

func (e *errRespStatusCodeNotSuccess) Is(target error) bool {
	return target == ErrRespStatusCodeNotSuccess
}

type errRetryDeadlineExceeded struct {
	maxRetryDuration time.Duration
}

func (e *errRetryDeadlineExceeded) Error() string {
	return prefixErrRetryDeadlineExceeded + e.maxRetryDuration.String()
}

func (e *errRetryDeadlineExceeded) Is(target error) bool {
	return target == ErrRetryDeadlineExceeded
}

type contextWrapper interface {
	WrapContext(context.Context) context.Context
	CleanupWrappedContext()
}

// TODO: http 431 error responses in server impl - https://www.rfc-editor.org/rfc/rfc6585.html#section-5

// Client should be initialized via NewClient()
type Client struct {
	baseReq http.Request
	cfg     clientConfig
}

type sharedConfig struct {
	authAdder               AuthAdder
	reqBodBufPool           RequestBodyBufferPool
	respBodBufPool          ResponseBodyBufferPool
	httpClient              http.Client
	method, path, userAgent string
	header                  http.Header
	query                   url.Values
	perCallTimeout          time.Duration
	retryBaseDelay          time.Duration
	retryMaxDelay           time.Duration
	retryMaxTimeout         time.Duration
	retryEnabled            bool
	setProtocolResponse     bool
	cowDoneHeader           bool
	cowDoneQuery            bool
}

type clientConfig struct {
	baseUrl        string
	serviceName    string
	middlewares    []RoundTripMiddleware
	contextWrapper contextWrapper

	sharedConfig

	baseUrlSet     bool
	serviceNameSet bool
}

type clientOpts struct{}
type ClientOption func(*clientConfig)

func ClientOpts() clientOpts {
	return clientOpts{}
}

type reqConfig struct {
	contextWrapper  contextWrapper
	body            any
	unmarshalJsonTo any
	getBody         func() (io.ReadCloser, error)
	renderType      renderType
	err             error

	sharedConfig

	getBodySet         bool
	unmarshalJsonToSet bool
}

type reqOpts struct{}
type ReqOption func(*reqConfig)

func ReqOpts() reqOpts {
	return reqOpts{}
}

// validate verifies after configuration merge/apply that all values set and the combinations
// of options are reasonable and merged/applied without conflicts/errors
//
// validate must only look for errors in configuration and perform minor
// request finalization where some configurations are optional yet defaults depend
// on other options
func (cfg *sharedConfig) validate() error {

	if cfg.perCallTimeout <= 0 {
		return ErrPerCallTimeoutMustBeGTZero
	}

	if len(cfg.method) == 0 || strings.IndexFunc(cfg.method, func(r rune) bool { return !httpguts.IsTokenRune(r) }) != -1 {
		return fmt.Errorf("%w: %s", ErrBadMethod, cfg.method)
	}

	if cfg.retryMaxTimeout == 0 {
		cfg.retryMaxTimeout = cfg.perCallTimeout
	}

	return nil
}

// validate verifies after configuration merge/apply that all values set and the combinations
// of options are reasonable and merged/applied without conflicts/errors
//
// validate must only look for errors in configuration and perform minor
// request finalization where some configurations are optional yet defaults depend
// on other options
func (cfg *clientConfig) validate() error {

	// validate shared options configuration intent
	if err := cfg.sharedConfig.validate(); err != nil {
		return err
	}

	if !cfg.baseUrlSet {
		return errors.New("base url not set")
	}

	if cfg.baseUrl == "" {
		return errors.New("base url set to empty string")
	}

	if cfg.serviceNameSet && cfg.serviceName == "" {
		return errors.New("service name cannot be set to an empty string")
	}

	return nil
}

// validate verifies after configuration merge/apply that all values set and the combinations
// of options are reasonable and merged/applied without conflicts/errors
//
// validate must only look for errors in configuration and perform minor
// request finalization where some configurations are optional yet defaults depend
// on other options
//
// For example, renderType is checked prior to calling this method and must not be changed in it.
func (cfg *reqConfig) validate() error {

	if cfg.err != nil {
		return cfg.err
	}

	if err := cfg.sharedConfig.validate(); err != nil {
		return err
	}

	if cfg.unmarshalJsonToSet && cfg.unmarshalJsonTo == nil {
		return ErrNilUnmarshalTarget
	}

	if cfg.retryEnabled && cfg.getBodySet && cfg.getBody == nil {
		return ErrNilGetBodyNotAllowedWhenRetrying
	}

	return nil
}

func (clientOpts) BaseURL(s string) ClientOption {
	return func(cfg *clientConfig) {
		cfg.baseUrl = s
		cfg.baseUrlSet = true
	}
}

func (clientOpts) ServiceName(s string) ClientOption {
	return func(cfg *clientConfig) {
		cfg.serviceName = s
		cfg.serviceNameSet = true
	}
}

func (clientOpts) Middlewares(m []RoundTripMiddleware) ClientOption {
	return func(cfg *clientConfig) {
		cfg.middlewares = m
		// cfg.middlewaresSet = true
		// not tracking if the user has overwritten things and set the middlewares to nil
		// because it is a valid way to disable the DefaultMiddlewares()
	}
}

func (clientOpts) ContextWrapper(cw contextWrapper) ClientOption {
	return func(cfg *clientConfig) {
		cfg.contextWrapper = cw
	}
}

func (reqOpts) ContextWrapper(cw contextWrapper) ReqOption {
	return func(cfg *reqConfig) {
		cfg.contextWrapper = cw
	}
}

func (fcr *FluentClientRequest) ContextWrapper(cw contextWrapper) *FluentClientRequest {
	fcr.cfg.contextWrapper = cw
	return fcr
}

type releasableBuffer interface {
	// Release releases the buffer resource back to a pool implementation
	Release()

	// methods similar in purpose to those present in bytes.Buffer

	io.Writer
	io.Reader
	Reset()
	Len() int
	Cap() int
	Bytes() []byte
}

type RequestBodyBufferPool interface {
	GetRequestBodyBuffer(*http.Request) interface{ releasableBuffer }
}

func (clientOpts) RequestBodyBufferPool(v RequestBodyBufferPool) ClientOption {
	return func(cfg *clientConfig) {
		cfg.reqBodBufPool = v
	}
}

func (reqOpts) RequestBodyBufferPool(v RequestBodyBufferPool) ReqOption {
	return func(cfg *reqConfig) {
		cfg.reqBodBufPool = v
	}
}

func (fcr *FluentClientRequest) RequestBodyBufferPool(v RequestBodyBufferPool) *FluentClientRequest {
	fcr.cfg.reqBodBufPool = v
	return fcr
}

type ResponseBodyBufferPool interface {
	GetResponseBodyBuffer(*http.Response) interface{ releasableBuffer }
}

func (clientOpts) ResponseBodyBufferPool(v ResponseBodyBufferPool) ClientOption {
	return func(cfg *clientConfig) {
		cfg.respBodBufPool = v
	}
}

func (reqOpts) ResponseBodyBufferPool(v ResponseBodyBufferPool) ReqOption {
	return func(cfg *reqConfig) {
		cfg.respBodBufPool = v
	}
}

func (fcr *FluentClientRequest) ResponseBodyBufferPool(v ResponseBodyBufferPool) *FluentClientRequest {
	fcr.cfg.respBodBufPool = v
	return fcr
}

// AuthAdder callers should consider implementing AuthAdderStrategyDescriber
// for the AuthAdder that they pass in if the strategy for adding authorization does not change over
// time.
//
// Callers should also consider implementing the AuthRefresher interface if the resource server can Reject the
// access token(s) specified in the AuthAdder strategy.
func (clientOpts) AuthAdder(authAdder AuthAdder) ClientOption {
	return func(cfg *clientConfig) {
		cfg.authAdder = authAdder
	}
}

// AuthAdder callers should consider implementing AuthAdderStrategyDescriber
// for the AuthAdder that they pass in if the strategy for adding authorization does not change over
// time.
//
// Callers should also consider implementing the AuthRefresher interface if the resource server can Reject the
// access token(s) specified in the AuthAdder strategy.
func (reqOpts) AuthAdder(authAdder AuthAdder) ReqOption {
	return func(cfg *reqConfig) {
		cfg.authAdder = authAdder
	}
}

// AuthAdder callers should consider implementing AuthAdderStrategyDescriber
// for the AuthAdder that they pass in if the strategy for adding authorization does not change over
// time.
//
// Callers should also consider implementing the AuthRefresher interface if the resource server can Reject the
// access token(s) specified in the AuthAdder strategy.
func (fcr *FluentClientRequest) AuthAdder(authAdder AuthAdder) *FluentClientRequest {
	fcr.cfg.authAdder = authAdder
	return fcr
}

// setHttpClient destructively sets the http client to be used in a request
//
// Ideally setHttpClient should not be called after setTransport, setCheckRedirect, or setJar.
func (cfg *sharedConfig) setHttpClient(c http.Client) {
	cfg.httpClient = c
}

// HttpClient destructively sets the http client to be used in a request
//
// Ideally HttpClient should not be called after Transport, CheckRedirect, or Jar.
func (clientOpts) HttpClient(c http.Client) ClientOption {
	return func(cfg *clientConfig) {
		cfg.setHttpClient(c)
	}
}

// HttpClient destructively sets the http client to be used in a request
//
// Ideally HttpClient should not be called after Transport, CheckRedirect, or Jar.
func (reqOpts) HttpClient(c http.Client) ReqOption {
	return func(cfg *reqConfig) {
		cfg.setHttpClient(c)
	}
}

// HttpClient destructively sets the http client to be used in a request
//
// Ideally HttpClient should not be called after Transport, CheckRedirect, or Jar.
func (fcr *FluentClientRequest) HttpClient(c http.Client) *FluentClientRequest {
	fcr.cfg.setHttpClient(c)
	return fcr
}

// setTransport sets the RoundTripper implementation to be used in the http request
//
// Ideally setHttpClient should not be called after setTransport.
func (cfg *sharedConfig) setTransport(t http.RoundTripper) {
	cfg.httpClient.Transport = t
}

// Transport sets the RoundTripper implementation to be used in the http request
//
// Ideally HttpClient should not be called after Transport.
func (clientOpts) Transport(t http.RoundTripper) ClientOption {
	return func(cfg *clientConfig) {
		cfg.setTransport(t)
	}
}

// Transport sets the RoundTripper implementation to be used in the http request
//
// Ideally HttpClient should not be called after Transport.
func (reqOpts) Transport(t http.RoundTripper) ReqOption {
	return func(cfg *reqConfig) {
		cfg.setTransport(t)
	}
}

// Transport sets the RoundTripper implementation to be used in the http request
//
// Ideally HttpClient should not be called after Transport.
func (fcr *FluentClientRequest) Transport(t http.RoundTripper) *FluentClientRequest {
	fcr.cfg.setTransport(t)
	return fcr
}

// setCheckRedirect sets the redirection checking implementation to be used in the http request
//
// Ideally setHttpClient should not be called after setCheckRedirect.
func (cfg *sharedConfig) setCheckRedirect(f func(req *http.Request, via []*http.Request) error) {
	cfg.httpClient.CheckRedirect = f
}

// CheckRedirect sets the redirection checking implementation to be used in the http request
//
// Ideally HttpClient should not be called after CheckRedirect.
func (clientOpts) CheckRedirect(f func(req *http.Request, via []*http.Request) error) ClientOption {
	return func(cfg *clientConfig) {
		cfg.setCheckRedirect(f)
	}
}

// CheckRedirect sets the redirection checking implementation to be used in the http request
//
// Ideally HttpClient should not be called after CheckRedirect.
func (reqOpts) CheckRedirect(f func(req *http.Request, via []*http.Request) error) ReqOption {
	return func(cfg *reqConfig) {
		cfg.setCheckRedirect(f)
	}
}

// CheckRedirect sets the redirection checking implementation to be used in the http request
//
// Ideally HttpClient should not be called after CheckRedirect.
func (fcr *FluentClientRequest) CheckRedirect(f func(req *http.Request, via []*http.Request) error) *FluentClientRequest {
	fcr.cfg.setCheckRedirect(f)
	return fcr
}

// setJar sets the cookie jar implementation to be used in the http request
//
// Ideally setHttpClient should not be called after setJar.
func (cfg *sharedConfig) setJar(j http.CookieJar) {
	cfg.httpClient.Jar = j
}

// Jar sets the cookie jar implementation to be used in the http request
//
// Ideally HttpClient should not be called after Jar.
func (clientOpts) Jar(j http.CookieJar) ClientOption {
	return func(cfg *clientConfig) {
		cfg.setJar(j)
	}
}

// Jar sets the cookie jar implementation to be used in the http request
//
// Ideally HttpClient should not be called after Jar.
func (reqOpts) Jar(j http.CookieJar) ReqOption {
	return func(cfg *reqConfig) {
		cfg.setJar(j)
	}
}

// Jar sets the cookie jar implementation to be used in the http request
//
// Ideally HttpClient should not be called after Jar.
func (fcr *FluentClientRequest) Jar(j http.CookieJar) *FluentClientRequest {
	fcr.cfg.setJar(j)
	return fcr
}

func (cfg *sharedConfig) setUserAgent(s string) {
	cfg.userAgent = s
}

func (clientOpts) UserAgent(s string) ClientOption {
	return func(cfg *clientConfig) {
		cfg.setUserAgent(s)
	}
}

func (cfg ReqOption) UserAgent(s string) ReqOption {
	return func(cfg *reqConfig) {
		cfg.setUserAgent(s)
	}
}

func closePrevAndNextBody(bodyPtr *any, pr *io.Reader) (prevCloseErr, closeErr error) {
	var r io.Reader
	if pr != nil {
		if r = *pr; r != nil {
			*pr = nil
			if rc, ok := r.(io.ReadCloser); ok && rc != http.NoBody {
				defer func() {
					if err := rc.Close(); err != nil {
						closeErr = fmt.Errorf("new-body close: %w", err)
					}
				}()
			}
		}
	}

	if *bodyPtr != nil && *bodyPtr != r {
		b := *bodyPtr
		*bodyPtr = nil
		if rc, ok := b.(io.ReadCloser); ok && rc != http.NoBody {
			if err := rc.Close(); err != nil {
				if pr != nil {
					prevCloseErr = fmt.Errorf("prev-body close: %w", err)
				} else {
					prevCloseErr = err
				}
			}
		}
	}

	return
}

func (reqOpts) Body(r io.Reader) ReqOption {
	return func(cfg *reqConfig) {
		if cfg.renderType == readerRenderType {
			prevCloseErr, closeErr := closePrevAndNextBody(&cfg.body, &r)

			err := fmt.Errorf("from Body option: %w", ErrBadConfigReaderBodyOverrides)
			cfg.err = errors.Join(cfg.err, err, prevCloseErr, closeErr)
		}
		cfg.body = r
		cfg.renderType = readerRenderType
	}
}

func (fcr *FluentClientRequest) Body(r io.Reader) *FluentClientRequest {
	if fcr.cfg.renderType == readerRenderType {
		prevCloseErr, closeErr := closePrevAndNextBody(&fcr.cfg.body, &r)

		err := fmt.Errorf("from Body option of FluentClientRequest: %w", ErrBadConfigReaderBodyOverrides)
		fcr.cfg.err = errors.Join(fcr.cfg.err, err, prevCloseErr, closeErr)
	}
	fcr.cfg.body = r
	fcr.cfg.renderType = readerRenderType
	return fcr
}

func (reqOpts) BodyAndGetter(r io.Reader, f func() (io.ReadCloser, error)) ReqOption {
	return func(cfg *reqConfig) {
		if cfg.renderType == readerRenderType {
			f = nil
			prevCloseErr, closeErr := closePrevAndNextBody(&cfg.body, &r)

			err := fmt.Errorf("from BodyAndGetter option: %w", ErrBadConfigReaderBodyOverrides)
			cfg.err = errors.Join(cfg.err, err, prevCloseErr, closeErr)
		}
		cfg.body = r
		cfg.getBody = f
		cfg.getBodySet = true
		cfg.renderType = readerRenderType
	}
}

func (fcr *FluentClientRequest) BodyAndGetter(r io.Reader, f func() (io.ReadCloser, error)) *FluentClientRequest {
	if fcr.cfg.renderType == readerRenderType {
		f = nil
		prevCloseErr, closeErr := closePrevAndNextBody(&fcr.cfg.body, &r)

		err := fmt.Errorf("from BodyAndGetter option of FluentClientRequest: %w", ErrBadConfigReaderBodyOverrides)
		fcr.cfg.err = errors.Join(fcr.cfg.err, err, prevCloseErr, closeErr)
	}
	fcr.cfg.body = r
	fcr.cfg.getBody = f
	fcr.cfg.getBodySet = true
	fcr.cfg.renderType = readerRenderType
	return fcr
}

func (cfg *sharedConfig) setPerCallTimeout(d time.Duration) {
	cfg.perCallTimeout = d
}

func (clientOpts) PerCallTimeout(d time.Duration) ClientOption {
	return func(cfg *clientConfig) {
		cfg.setPerCallTimeout(d)
	}
}

func (reqOpts) PerCallTimeout(d time.Duration) ReqOption {
	return func(cfg *reqConfig) {
		cfg.setPerCallTimeout(d)
	}
}

func (fcr *FluentClientRequest) PerCallTimeout(d time.Duration) *FluentClientRequest {
	fcr.cfg.setPerCallTimeout(d)
	return fcr
}

func (cfg *sharedConfig) setRetryMaxTimeout(d time.Duration) {
	if d < 0 {
		d = 0
	}
	cfg.retryMaxTimeout = d
}

func (clientOpts) RetryMaxTimeout(d time.Duration) ClientOption {
	return func(cfg *clientConfig) {
		cfg.setRetryMaxTimeout(d)
	}
}

func (reqOpts) RetryMaxTimeout(d time.Duration) ReqOption {
	return func(cfg *reqConfig) {
		cfg.setRetryMaxTimeout(d)
	}
}

func (fcr *FluentClientRequest) RetryMaxTimeout(d time.Duration) *FluentClientRequest {
	fcr.cfg.setRetryMaxTimeout(d)
	return fcr
}

func (reqOpts) JsonBody(b any) ReqOption {
	return func(cfg *reqConfig) {

		if cfg.renderType == readerRenderType {
			prevCloseErr, _ := closePrevAndNextBody(&cfg.body, nil)

			err := fmt.Errorf("from JsonBody option: %w", ErrBadConfigReaderBodyOverrides)
			cfg.err = errors.Join(cfg.err, err, prevCloseErr)
		}
		cfg.body = b
		cfg.renderType = jsonRenderType
	}
}

func (fcr *FluentClientRequest) JsonBody(b any) *FluentClientRequest {
	if fcr.cfg.renderType == readerRenderType {
		prevCloseErr, _ := closePrevAndNextBody(&fcr.cfg.body, nil)

		err := fmt.Errorf("from JsonBody option of FluentClientRequest: %w", ErrBadConfigReaderBodyOverrides)
		fcr.cfg.err = errors.Join(fcr.cfg.err, err, prevCloseErr)
	}
	fcr.cfg.body = b
	fcr.cfg.renderType = jsonRenderType
	return fcr
}

// setHeader destructively sets the http header contents
//
// Ideally setHeader should not be called after addHeader or addHeaders.
func (cfg *sharedConfig) setHeader(h http.Header) {
	if len(h) == 0 {
		cfg.header = nil
	} else {
		cfg.header = h.Clone()
	}
	cfg.cowDoneHeader = true
}

// Header destructively sets the http header contents
//
// Ideally Header should not be called after addHeader or addHeaders.
func (clientOpts) Header(h http.Header) ClientOption {
	return func(cfg *clientConfig) {
		cfg.setHeader(h)
	}
}

// Header destructively sets the http header contents
//
// Ideally Header should not be called after addHeader or addHeaders.
func (reqOpts) Header(h http.Header) ReqOption {
	return func(cfg *reqConfig) {
		cfg.setHeader(h)
	}
}

// Header destructively sets the http header contents
//
// Ideally Header should not be called after addHeader or addHeaders.
func (fcr *FluentClientRequest) Header(h http.Header) *FluentClientRequest {
	fcr.cfg.setHeader(h)
	return fcr
}

// addHeader adds a header key-value pair to the current http header
//
// Ideally setHeader should not be called after addHeader.
func (cfg *sharedConfig) addHeader(k, v string) {
	if cfg.header == nil {
		cfg.header = http.Header{}
	} else if !cfg.cowDoneHeader {
		cfg.header = cfg.header.Clone()
	}
	cfg.cowDoneHeader = true

	cfg.header.Set(k, v)
}

// AddHeader adds a header key-value pair to the current http header
//
// Ideally Header should not be called after addHeader.
func (clientOpts) AddHeader(k, v string) ClientOption {
	return func(cfg *clientConfig) {
		cfg.addHeader(k, v)
	}
}

// AddHeader adds a header key-value pair to the current http header
//
// Ideally Header should not be called after addHeader.
func (reqOpts) AddHeader(k, v string) ReqOption {
	return func(cfg *reqConfig) {
		cfg.addHeader(k, v)
	}
}

// AddHeader adds a header key-value pair to the current http header
//
// Ideally Header should not be called after addHeader.
func (fcr *FluentClientRequest) AddHeader(k, v string) *FluentClientRequest {
	fcr.cfg.addHeader(k, v)
	return fcr
}

// addHeaders adds multiple key-value pairs to the current http header
//
// Ideally setHeader should not be called after addHeaders.
func (cfg *sharedConfig) addHeaders(h http.Header) {
	if len(h) == 0 {
		return
	}

	h = h.Clone()

	if cfg.header == nil {
		cfg.header = h
		cfg.cowDoneHeader = true
	} else {
		if !cfg.cowDoneHeader {
			cfg.header = cfg.header.Clone()
			cfg.cowDoneHeader = true
		}

		for k, v := range h {
			cfg.header[k] = v
		}
	}
}

// AddHeaders adds multiple key-value pairs to the current http header
//
// Ideally Header should not be called after addHeaders.
func (clientOpts) AddHeaders(h http.Header) ClientOption {
	return func(cfg *clientConfig) {
		cfg.addHeaders(h)
	}
}

// AddHeaders adds multiple key-value pairs to the current http header
//
// Ideally Header should not be called after addHeaders.
func (reqOpts) AddHeaders(h http.Header) ReqOption {
	return func(cfg *reqConfig) {
		cfg.addHeaders(h)
	}
}

// AddHeaders adds multiple key-value pairs to the current http header
//
// Ideally Header should not be called after addHeaders.
func (fcr *FluentClientRequest) AddHeaders(h http.Header) *FluentClientRequest {
	fcr.cfg.addHeaders(h)
	return fcr
}

// setQuery destructively sets the http request url query values
//
// Ideally setQuery should not be called after addQuery or addQueries.
func (cfg *sharedConfig) setQuery(v url.Values) {
	cfg.query = cloneMapStrSlice(v)
	cfg.cowDoneQuery = true
}

func (clientOpts) Query(v url.Values) ClientOption {
	return func(cfg *clientConfig) {
		cfg.setQuery(v)
	}
}

func (reqOpts) Query(v url.Values) ReqOption {
	return func(cfg *reqConfig) {
		cfg.setQuery(v)
	}
}

func (fcr *FluentClientRequest) Query(v url.Values) *FluentClientRequest {
	fcr.cfg.setQuery(v)
	return fcr
}

func (cfg *sharedConfig) addQuery(k, v string) {
	if cfg.query == nil {
		cfg.query = url.Values{}
	} else if !cfg.cowDoneQuery {
		cfg.query = cloneMapStrSlice(cfg.query)
	}
	cfg.cowDoneQuery = true

	cfg.query.Set(k, v)
}

func (clientOpts) AddQuery(k, v string) ClientOption {
	return func(cfg *clientConfig) {
		cfg.addQuery(k, v)
	}
}

func (reqOpts) AddQuery(k, v string) ReqOption {
	return func(cfg *reqConfig) {
		cfg.addQuery(k, v)
	}
}

func (fcr *FluentClientRequest) AddQuery(k, v string) *FluentClientRequest {
	fcr.cfg.addQuery(k, v)
	return fcr
}

// cloneMapStrSlice is basically copied from go's http package: "func (h Header) Clone() Header"
//
// except that not just nil input return nil, empty input return nil too
func cloneMapStrSlice(h map[string][]string) map[string][]string {
	if len(h) == 0 {
		return nil
	}

	// Find total number of values.
	nv := 0
	for _, vv := range h {
		nv += len(vv)
	}
	sv := make([]string, nv) // shared backing array for headers' values
	h2 := make(map[string][]string, len(h))
	for k, vv := range h {
		if vv == nil {
			// Preserve nil values. ReverseProxy distinguishes
			// between nil and zero-length header values.
			h2[k] = nil
			continue
		}
		n := copy(sv, vv)
		h2[k] = sv[:n:n]
		sv = sv[n:]
	}
	return h2
}

func (cfg *sharedConfig) addQueries(v url.Values) {
	if len(v) == 0 {
		return
	}

	v = url.Values(cloneMapStrSlice(v))

	if cfg.query == nil {
		cfg.query = v
		cfg.cowDoneQuery = true
	} else {
		if !cfg.cowDoneQuery {
			cfg.query = cloneMapStrSlice(cfg.query)
			cfg.cowDoneQuery = true
		}

		for k, v := range v {
			cfg.query[k] = v
		}
	}
}

func (clientOpts) AddQueries(v url.Values) ClientOption {
	return func(cfg *clientConfig) {
		cfg.addQueries(v)
	}
}

func (reqOpts) AddQueries(v url.Values) ReqOption {
	return func(cfg *reqConfig) {
		cfg.addQueries(v)
	}
}

func (fcr *FluentClientRequest) AddQueries(v url.Values) *FluentClientRequest {
	fcr.cfg.addQueries(v)
	return fcr
}

func (cfg *sharedConfig) setPath(p string) {
	cfg.path = p
}

func (clientOpts) Path(p string) ClientOption {
	return func(cfg *clientConfig) {
		cfg.setPath(p)
	}
}

func (reqOpts) Path(p string) ReqOption {
	return func(cfg *reqConfig) {
		cfg.setPath(p)
	}
}

func (fcr *FluentClientRequest) Path(p string) *FluentClientRequest {
	fcr.cfg.setPath(p)
	return fcr
}

func (cfg *sharedConfig) setMethod(m string) {
	cfg.method = m
}

func (clientOpts) Method(m string) ClientOption {
	return func(cfg *clientConfig) {
		cfg.setMethod(m)
	}
}

func (reqOpts) Method(m string) ReqOption {
	return func(cfg *reqConfig) {
		cfg.setMethod(m)
	}
}

func (fcr *FluentClientRequest) Method(m string) *FluentClientRequest {
	fcr.cfg.setMethod(m)
	return fcr
}

// UnmarshalJSONResp only has an effect in a Do context.
//
// Using it during New will result in an error.
func (reqOpts) UnmarshalJSONRespTo(v any) ReqOption {
	return func(cfg *reqConfig) {
		cfg.unmarshalJsonTo = v
		cfg.unmarshalJsonToSet = true
	}
}

// UnmarshalJSONResp only has an effect in a Do context.
//
// Using it during New will result in an error.
func (fcr *FluentClientRequest) UnmarshalJSONRespTo(v any) *FluentClientRequest {
	fcr.cfg.unmarshalJsonTo = v
	fcr.cfg.unmarshalJsonToSet = true
	return fcr
}

func (cfg *sharedConfig) retry(b bool) {
	cfg.retryEnabled = b
}

func (clientOpts) Retry(b bool) ClientOption {
	return func(cfg *clientConfig) {
		cfg.retry(b)
	}
}

func (reqOpts) Retry(b bool) ReqOption {
	return func(cfg *reqConfig) {
		cfg.retry(b)
	}
}

func (fcr *FluentClientRequest) Retry(b bool) *FluentClientRequest {
	fcr.cfg.retry(b)
	return fcr
}

func (cfg *sharedConfig) iSetProtocolResponse(b bool) {
	cfg.setProtocolResponse = b
}

func (clientOpts) SetProtocolResponse(b bool) ClientOption {
	return func(cfg *clientConfig) {
		cfg.iSetProtocolResponse(b)
	}
}

func (reqOpts) SetProtocolResponse(b bool) ReqOption {
	return func(cfg *reqConfig) {
		cfg.iSetProtocolResponse(b)
	}
}

func (fcr *FluentClientRequest) SetProtocolResponse(b bool) *FluentClientRequest {
	fcr.cfg.iSetProtocolResponse(b)
	return fcr
}

func (c *Client) reqConfig() reqConfig {
	var cfg reqConfig
	cfg.sharedConfig = c.cfg.sharedConfig
	if len(cfg.sharedConfig.header) == 0 {
		cfg.sharedConfig.header = nil
		cfg.cowDoneHeader = true
	} else {
		cfg.cowDoneHeader = false
	}
	if len(cfg.sharedConfig.query) == 0 {
		cfg.sharedConfig.query = nil
		cfg.cowDoneQuery = true
	} else {
		cfg.cowDoneQuery = false
	}
	return cfg
}

func (c *Client) CloseIdleConnections() {
	type closeIdler interface {
		CloseIdleConnections()
	}
	if tr, ok := c.cfg.httpClient.Transport.(closeIdler); ok {
		tr.CloseIdleConnections()
	}
}

var notNegativeIntRegex = regexp.MustCompile(`^(?:0|[1-9][0-9]*)$`)

func canRetryAfter(resp *http.Response) (time.Duration, bool) {
	if resp == nil {
		return 0, false
	}

	switch resp.StatusCode {
	// https://developer.mozilla.org/en-US/docs/Web/HTTP/Status/429
	// https://developer.mozilla.org/en-US/docs/Web/HTTP/Status/501
	// https://developer.mozilla.org/en-US/docs/Web/HTTP/Status/503
	case http.StatusTooManyRequests, http.StatusNotImplemented, http.StatusServiceUnavailable:
		if v, ok := resp.Header["Retry-After"]; ok && len(v) > 0 {
			if v := strings.TrimSpace(v[0]); v != "" {
				if notNegativeIntRegex.MatchString(v) {
					if v, err := strconv.Atoi(v); err == nil && v >= 0 {
						delay := time.Duration(v) * time.Second
						return delay, true
					}
				} else if v, err := time.Parse(time.RFC1123, v); err == nil {
					delay := time.Until(v)
					if delay < 0 {
						delay = 0
					}
					return delay, true
				}
			}
		}
	}

	return 0, false
}

func respStatusSuccess(resp *http.Response) bool {
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

func canRetryStatusCode(resp *http.Response) bool {
	if resp == nil {
		return false
	}

	if resp.StatusCode >= 500 && resp.StatusCode < 600 {
		// https://developer.mozilla.org/en-US/docs/Web/HTTP/Status/501
		return resp.StatusCode != http.StatusNotImplemented || (resp.Request.Method != http.MethodGet && resp.Request.Method != http.MethodHead)
	}

	return false
}

func isIdempotentMethod(method string) bool {
	// https://www.rfc-editor.org/rfc/rfc9110.html#name-safe-methods
	// https://www.rfc-editor.org/rfc/rfc9110.html#name-idempotent-methods

	switch method {
	case /* "safe" read-only methods (no resource state-change effects) */ http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace /* "idempotent methods" with resource state-change effects that can be replayed safely (assuming request order holds) */, http.MethodPut, http.MethodDelete:
		return true
	}

	return false
}

func hasIdempotencyHeaderKey(h http.Header) bool {
	// The Idempotency-Key, while non-standard, is widely used to
	// mean a POST or other request is idempotent. See
	// https://golang.org/issue/19943#issuecomment-421092421

	// internally net/http/header.go is using header.has which is as simple as:
	// func (h Header) has(key string) bool {
	// 	_, ok := h[key]
	// 	return ok
	// }

	if h == nil {
		return false
	}

	keys := []string{"Idempotency-Key", "X-Idempotency-Key"}

	for _, k := range keys {
		if _, ok := h[k]; ok {
			return true
		}
	}

	return false
}

func canRetryConnErr(err error) bool {
	netOpErr := &net.OpError{}
	if !errors.As(err, &netOpErr) {
		return false
	}

	switch netOpErr.Op {
	case "dial", "proxyconnect":
		if !errors.Is(netOpErr.Err, &net.AddrError{}) {
			return true
		}
	}

	return false
}

// Do issues a http request and returns a *Response.
// It also guarantees that the response body is closed before the calling context resumes.
func (c *Client) Do(ctx context.Context, options ...ReqOption) (*ClientResponse, error) {
	const autoCloseRespBody = true

	_, resp, err := c.do(ctx, autoCloseRespBody, options...)
	return resp, err
}

// Do issues a http request and returns a *Response.
// It also guarantees that the response body is closed before the calling context resumes.
func (fcr *FluentClientRequest) Do(ctx context.Context) (*ClientResponse, error) {
	const autoCloseRespBody = true

	fcr.closed = true
	_, resp, err := fcr.c.do(ctx, autoCloseRespBody) // TODO: ensure nil options slice does not add allocations
	return resp, err
}

// DoStandard behaves exactly like Do except it returns a go standard *http.Response and it is the caller's responsibility to close the response body.
//
// Typically Do should be called instead of this function.
func (c *Client) DoStandard(ctx context.Context, options ...ReqOption) (*http.Response, error) {
	const autoCloseRespBody = false

	stdResp, _, err := c.do(ctx, autoCloseRespBody, options...)
	return stdResp, err
}

// DoStandard behaves exactly like Do except it returns a go standard *http.Response and it is the caller's responsibility to close the response body.
//
// Typically Do should be called instead of this function.
func (fcr *FluentClientRequest) DoStandard(ctx context.Context) (*http.Response, error) {
	const autoCloseRespBody = false

	fcr.closed = true
	stdResp, _, err := fcr.c.do(ctx, autoCloseRespBody) // TODO: ensure nil options slice does not add allocations
	return stdResp, err
}

// Close should only be called from the same thread that would call Do under normal circumstances
func (fcr *FluentClientRequest) Close() error {
	if fcr.closed {
		return fcr.closeErr
	}
	fcr.closed = true

	if cw := fcr.c.cfg.contextWrapper; cw != nil {
		defer cw.CleanupWrappedContext()
	}

	if cw := fcr.cfg.contextWrapper; cw != nil {
		defer cw.CleanupWrappedContext()
	}

	if fcr.cfg.renderType == readerRenderType && fcr.cfg.body != nil && fcr.cfg.body != http.NoBody {
		if rc, ok := fcr.cfg.body.(io.ReadCloser); ok {
			if err := rc.Close(); err != nil {
				fcr.closeErr = err
				return err
			}
		}
	}

	return nil
}

// FluentClientRequest marries the context of a http client with a request configuration and the
// relevant defaults from the parent client configuration. All methods on this type
// are "builder" methods that return the reference to the same "self" for ease of configuration
// chaining EXCEPT for Do, DoStandard, and Close. When either Do or DoStandard are called the FluentClientRequest
// reference is no longer valid to use. Close is a convenience method that should be called at most once and
// is only necessary for the case where a request is getting built, but is never performed via one of the Do*
// methods.
//
// FluentClientRequest cannot be used in its zero value state.
//
// A valid instance can only be gained by calling NewFluentReq() on a Client instance reference.
type FluentClientRequest struct {
	c        *Client
	cfg      reqConfig
	closeErr error
	closed   bool
}

func (c *Client) NewFluentReq() *FluentClientRequest {
	return &FluentClientRequest{
		c:   c,
		cfg: c.reqConfig(),
	}
}

func (c *Client) do(ctx context.Context, autoCloseRespBody bool, options ...ReqOption) (*http.Response, *ClientResponse, error) {
	cfg := c.reqConfig()

	firstNilOptIndex := -1

	// Process options and avoid panic'ing until later
	// if there is a nil option.
	//
	// This is done because we might need to release some
	// resources before this error context returns.
	for i, f := range options {
		if f != nil {
			f(&cfg)
		} else if firstNilOptIndex == -1 {
			firstNilOptIndex = i
		}
	}

	rr := newReqRunner(c, &cfg, autoCloseRespBody)
	return rr.run(ctx, firstNilOptIndex)
}

func (c *Client) newReq(method, path string) *http.Request {
	ctx := context.Background()
	req := c.baseReq.Clone(ctx)
	req.Method = method

	if path == "" {
		return req
	}

	// workaround required until this issue is fixed: https://github.com/golang/go/issues/58605
	//
	// note that because req is a clone already it can be modified without creating side-effects
	if req.URL.Path == "" {
		req.URL.Path = "/"
	}

	req.URL = req.URL.JoinPath(path)

	return req
}

type buffer interface {
	io.Reader
	io.Writer
	Reset()
	Len() int
	Bytes() []byte
}

type erroredNopReader struct {
	err error
	buf interface {
		io.Reader
		Reset()
		Len() int
		Bytes() []byte
	}
}

func (enr erroredNopReader) WriteTo(w io.Writer) (int64, error) {
	m := enr.buf.Len()

	if m > 0 {
		defer enr.buf.Reset()

		n, err := w.Write(enr.buf.Bytes())
		if n > m {
			panic("erroredNopReader.WriteTo: invalid Write count")
		}
		if err != nil {
			return int64(n), err
		}

		if n != m {
			return int64(n), io.ErrShortWrite
		}
	}

	return int64(m), enr.err
}

func (enr erroredNopReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	n, err := enr.buf.Read(p)
	if err != nil && errors.Is(err, io.EOF) {
		err = enr.err
	}

	return n, err
}

func (enr erroredNopReader) Close() error {
	return nil
}

func (enr erroredNopReader) IsNopCloser() bool {
	return true
}

func bufferAndCloseBody(ctx context.Context, bufPool ResponseBodyBufferPool, r *http.Response, whence string) interface {
	io.Reader
	Len() int
	Bytes() []byte
} {
	if r == nil || r.Body == nil {
		return nil
	}

	b := r.Body
	r.Body = nil
	defer func() {
		if err := b.Close(); err != nil {
			const logLevel = slog.LevelDebug
			if logf := xslog.DefaultFactory(); logf.Enabled(ctx, logLevel) {
				logf.Logger(ctx).WithErr(ctx, err).Log(ctx, logLevel,
					"failed to close response body",
					slog.String("whence", whence),
					slog.String("impact", "connection will be closed rather than placed back into pool"),
				)
			}
		}
	}()

	var buf buffer
	if bufPool != nil {

		// A response body buffer pool can simply implement a kind of response
		// size buffering bucket strategy like the following.

		// bufSize := -1
		// if cl := r.Header["Content-Length"]; len(cl) == 1 {
		// 	if v := cl[0]; len(v) > 0 {
		// 		if v, err := strconv.Atoi(v); err == nil && v >= 0 {
		// 			bufSize = v
		// 		}
		// 	}
		// }

		buf = bufPool.GetResponseBodyBuffer(r)
		// TODO: craft a way for a ClientResponse to be signaled that the
		// response body is no longer relevant and references should be
		// re-pooled.
	} else {

		buf = &bytes.Buffer{}
	}

	defer func() {
		if r.Body != nil {
			return
		}
		r.Body = erroredNopReader{ErrPanicInCopy, buf}
		xslog.DefaultFactory().Logger(ctx).SpanErr(ctx, ErrPanicInCopy,
			"failed to buffer response body",
			slog.String("whence", whence),
		)
	}()

	_, err := io.Copy(buf, b)
	if err != nil {
		r.Body = erroredNopReader{err, buf}
		xslog.DefaultFactory().Logger(ctx).SpanErr(ctx, err,
			"failed to buffer response body",
			slog.String("whence", whence),
		)
		return nil
	}

	r.Body = xio.NopCloser(buf)
	return buf
}

type RoundTripMiddleware = func(http.RoundTripper) http.RoundTripper

func DefaultRequestMiddlewares() []RoundTripMiddleware {
	return []RoundTripMiddleware{
		func(next http.RoundTripper) http.RoundTripper {
			return otelhttp.NewTransport(
				next,
				otelhttp.WithClientTrace(func(ctx context.Context) *httptrace.ClientTrace {
					return otelhttptrace.NewClientTrace(ctx)
				}),
			)
		},
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func wrapTransport(t http.RoundTripper, middlewares []RoundTripMiddleware) http.RoundTripper {
	if t == nil {
		if len(middlewares) == 0 {
			return nil
		}

		t = http.DefaultTransport
	}

	for i := len(middlewares) - 1; i >= 0; i-- {
		t = middlewares[i](t)
	}

	return t
}

var (
	defaultRequestMiddlewares = DefaultRequestMiddlewares()

	// Note that it may be counter intuitive to initialize
	// the header on the reqBasis value, however because
	// on each request the baseReq is cloned it saves an
	// an allocation for each round-trip cycle.
	//
	// In addition to the above it also simplifies code elsewhere
	// that would need to initialize the header in a JIT fashion.

	reqBasis = http.Request{
		Proto:      "HTTP/1.1",
		ProtoMajor: 1,
		ProtoMinor: 1,
		Header:     make(http.Header),
		Body:       http.NoBody,
		GetBody:    getNoBody,
	}
)

func NewClient(options ...ClientOption) (*Client, error) {

	cfg := clientConfig{
		middlewares: defaultRequestMiddlewares,
		sharedConfig: sharedConfig{
			method:              http.MethodGet,
			perCallTimeout:      10 * time.Second,
			retryBaseDelay:      1 * time.Millisecond,
			retryMaxDelay:       500 * time.Millisecond,
			retryEnabled:        true,
			setProtocolResponse: true,
		},
	}

	for _, f := range options {
		f(&cfg)
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	u, err := url.Parse(cfg.baseUrl)
	if err != nil {
		return nil, err
	}

	// normalize the host in the URL and request if it ends in an empty part value after the host-port delimiter
	if h, p, err := net.SplitHostPort(u.Host); err == nil && p == "" {
		u.Host = h
	}

	baseReq := reqBasis
	// baseReq.Method = http.MethodGet // intentionally left empty - will be initialized via a newReq call
	baseReq.URL = u
	baseReq.Host = u.Host

	cfg.httpClient.Transport = wrapTransport(cfg.httpClient.Transport, cfg.middlewares)

	return &Client{baseReq, cfg}, nil
}

func getNoBody() (io.ReadCloser, error) {
	return http.NoBody, nil
}
