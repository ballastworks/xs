package xhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
	"time"

	"github.com/ballastworks/xs/internal/xi_io"
	"github.com/ballastworks/xs/xcontext"
	"github.com/ballastworks/xs/xcontext/xspan"
	"github.com/ballastworks/xs/xio"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
	"go.opentelemetry.io/otel/trace"
)

var (
	ErrEmptyJSONRequestBody = errors.New("empty json request body")
	ErrStaticAuthFailed     = errors.New("static auth strategy failed")
	ErrAddAuthFailed        = errors.New("adding auth to request failed")
	errBadPath              = errors.New("bad request path")
)

// TODO: bufferAndCloseBody should not allow for an infinite buffer size by default
//
// It's easily a denial of service vector.

type reqRunnerResp struct {
	r  *http.Response
	cr *ClientResponse
}

// firstErrResponse is the first error response returned during a retry enabled
// context where a response is truly a fully parsed response header from the
// server.
//
// The first error response is used because I do not want to risk hiding real
// initial issues when given a full response header from the server.
//
// Newer issues may include rate limiting or other outage type responses at the
// application level because of other transient issues.
//
// But honestly, the first error response should be a real server-side response,
// so no connection level foolery will be reported if we can avoid doing so.
type firstErrResponse struct {
	r                *http.Response
	errDo            error
	errReadBody      error
	errUnmarshal     error
	hasErrStatusCode bool
}

type reqRunner struct {
	c   *Client
	cfg *reqConfig
	req *http.Request

	reqBodyBufReleaser interface{ Release() }

	authRefresher          authRefresher
	authAdder              AuthAdder
	protocolResponseSetter any

	lifecycleContext context.Context

	setupSpan    trace.Span
	reqSpan      trace.Span
	teardownSpan trace.Span

	firstErrResp firstErrResponse

	errAuthPreDo error
	errDo        error
	errReadBody  error
	errUnmarshal error
	errRetry     error

	retryAfter time.Duration

	resp         reqRunnerResp
	teardownErrs [6]error

	numTeardownErrs uint8

	canCheckAndRefreshAuth bool
	firstErrRespSet        bool
	hasErrStatusCode       bool
	refreshAuth            bool
	reqSpanStatSet         bool
	retryAfterOK           bool

	// move elements below here to bit flags

	autoCloseRespBody      bool
	cfgBodyClosed          bool
	endOfReqSpanReached    bool
	prsWantsClientResp     bool
	reqBodyClosed          bool
	reqIsIdempotentFlag    bool
	reqIsIdempotentFlagSet bool
	respBodyClosed         bool
}

func newReqRunner(c *Client, cfg *reqConfig, autoCloseRespBody bool) *reqRunner {
	return &reqRunner{c: c, cfg: cfg, autoCloseRespBody: autoCloseRespBody}
}

func (rr *reqRunner) run(ctx context.Context, firstNilOptIndex int) (_stdResp *http.Response, _clientResp *ClientResponse, _err error) {
	{
		var span trace.Span
		ctx, span = otel.Tracer("").Start(ctx, "xhttp.client.request",
			trace.WithSpanKind(trace.SpanKindClient),
		)
		defer span.End()
	}

	defer rr.endTeardownSpan()
	defer rr.finalize(&_stdResp, &_clientResp, &_err)

	if cw := rr.c.cfg.contextWrapper; cw != nil {
		ctx = cw.WrapContext(ctx)
		defer cw.CleanupWrappedContext()
	}
	if cw := rr.cfg.contextWrapper; cw != nil {
		ctx = cw.WrapContext(ctx)
		defer cw.CleanupWrappedContext()
	}
	rr.lifecycleContext = ctx

	ctx, rr.setupSpan = otel.Tracer("").Start(ctx, "xhttp.request.setup",
		trace.WithSpanKind(trace.SpanKindClient),
	)

	// register close of config sourced request body if needed
	if rr.cfg.renderType == readerRenderType && rr.cfg.body != nil && rr.cfg.body != http.NoBody {
		defer rr.closeCfgBody()
	}

	if firstNilOptIndex != -1 {
		panic("nil option in do call at index " + strconv.Itoa(firstNilOptIndex))
	}

	if err := rr.preflightCheck(ctx); err != nil {
		return nil, nil, err
	}

	defer rr.closeReqBody()
	if err := rr.buildRequest(ctx); err != nil {
		return nil, nil, err
	}

	defer rr.callProtocolResponseSetter()
	rr.setProtocolResponseSetter()

	rr.endSetupSpanOK()

	ctx, rr.reqSpan = otel.Tracer("").Start(rr.lifecycleContext, "xhttp.request.do",
		trace.WithSpanKind(trace.SpanKindClient),
	)

	// TODO: use url template path if possible with host instead of just host
	reqSpanName := rr.req.Method + " " + rr.req.URL.Host

	if rr.cfg.retryEnabled {
		rr.doWithRetries(ctx, reqSpanName)

		rr.startTeardownSpan()

		// Handle the case where the response indicates the request cannot be
		// retried and is errored and the body needs to be closed because this do
		// call would return an error.
		//
		// Note that if errors readBody, unmarshal, or retry are not nil
		// then the body will already have been buffered if possible and closed,
		// so the only error flag to really check is do.
		//
		// if response body not closed,
		// and if there is a do error,
		// and if there is one to close,
		// then lets ensure it is closed.
		//
		// While doing the above, also determine if this is going to be an error
		// response or not
		if rr.errDo != nil {
			if !rr.respBodyClosed {
				bufferAndCloseBody(ctx, rr.cfg.respBodBufPool, rr.resp.r, "cleaning up a detected error response")
			}

			// fallthrough to error case
		} else if rr.errAuthPreDo != nil || rr.hasErrStatusCode || rr.errReadBody != nil || rr.errRetry != nil || rr.errUnmarshal != nil {

			// do nothing - fallthrough to error case
		} else {

			goto AFTER_RESPONSE
		}

		// If the response would be an error response, return the first error
		// response we observed.
		if rr.firstErrRespSet {
			fErr := &rr.firstErrResp

			rr.resp.r = fErr.r
			rr.errDo = fErr.errDo
			rr.hasErrStatusCode = fErr.hasErrStatusCode
			rr.errReadBody = fErr.errReadBody
			rr.errUnmarshal = fErr.errUnmarshal
		}

	} else {
		rr.doOnce(ctx, reqSpanName, time.Now())

		// TODO: if failure was due to bad auth allow for "retrying" once and
		// only once?
		//
		// If retries are disabled auth failures need to be handled manually by
		// the upper layers when it likely should be handled here.

		rr.startTeardownSpan()
	}

AFTER_RESPONSE:

	if rr.autoCloseRespBody || rr.prsWantsClientResp {
		rr.resp.cr = newClientResponse(rr.resp.r)
	}

	rr.endOfReqSpanReached = true
	return
}

func (rr *reqRunner) prepareReqBody(ctx context.Context) error {

	type buffer interface {
		io.Writer
		io.Reader
		Reset()
		Len() int
		Bytes() []byte
	}

	// TODO: consider not pre-serializing and buffering if retries are disabled

	// the first task of each render type is to prep the body and consume the config reference
	switch rr.cfg.renderType {
	case defaultRenderType:

		// do nothing, there is no body to be concerned with and GetBody will be set
	case jsonRenderType:
		var buf buffer
		if bp := rr.cfg.reqBodBufPool; bp != nil {
			v := bp.GetRequestBodyBuffer(rr.req)
			rr.reqBodyBufReleaser = v
			buf = v
		} else {
			buf = &bytes.Buffer{}
		}

		enc := json.NewEncoder(buf)
		enc.SetEscapeHTML(false)
		err := enc.Encode(rr.cfg.body)
		if err != nil {
			xspan.RecordError(ctx, err, "context canceled")
			return err
		}
		rr.req.ContentLength = int64(buf.Len())
		if rr.req.ContentLength != 0 {
			r := bytes.NewReader(buf.Bytes())
			rr.req.Body = xio.NopCloser(r)
			rr.req.GetBody = func() (io.ReadCloser, error) {
				{
					b := rr.req.Body
					rr.req.Body = nil
					if b != nil {
						xi_io.ReleaseNopCloser(b)
					}
				}

				r := bytes.NewReader(buf.Bytes())
				return xio.NopCloser(r), nil
			}

			rr.req.Header[headerKeyContentType] = []string{headerValueContentTypeJson}
		} else {
			// this is actually an invalid request path
			// so none of the book-keeping matters too much
			// but just to ensure there is a consistent state
			// setting values as if there is no body

			rr.reqBodyBufReleaser = nil
			rr.req.GetBody = getNoBody
			rr.req.Body = http.NoBody

			err := ErrEmptyJSONRequestBody
			xspan.RecordError(ctx, err, "")
			return err
		}
	case readerRenderType:

		if rr.cfg.getBodySet {
			rr.req.GetBody = rr.cfg.getBody
		}
		if bodyReader, ok := rr.cfg.body.(io.Reader); ok {
			switch v := bodyReader.(type) {
			case *bytes.Buffer:
				rr.cfgBodyClosed = true

				rr.req.ContentLength = int64(v.Len())
				if rr.req.ContentLength != 0 {
					if !rr.cfg.getBodySet {
						buf := v.Bytes()
						rr.req.GetBody = func() (io.ReadCloser, error) {
							{
								b := rr.req.Body
								rr.req.Body = nil
								if b != nil {
									xi_io.ReleaseNopCloser(b)
								}
							}

							r := bytes.NewReader(buf)
							return xio.NopCloser(r), nil
						}
						r := bytes.NewReader(buf)
						rr.req.Body = xio.NopCloser(r)
					} else {
						rr.req.Body = xio.NopCloser(v)
					}
				} else {
					if !rr.cfg.getBodySet {
						rr.req.GetBody = getNoBody
					}
					rr.req.Body = http.NoBody
				}
			case *bytes.Reader:
				rr.cfgBodyClosed = true

				rr.req.ContentLength = int64(v.Len())
				if rr.req.ContentLength != 0 {
					if !rr.cfg.getBodySet {
						snapshot := *v
						rr.req.GetBody = func() (io.ReadCloser, error) {
							{
								b := rr.req.Body
								rr.req.Body = nil
								if b != nil {
									xi_io.ReleaseNopCloser(b)
								}
							}

							r := snapshot
							return xio.NopCloser(&r), nil
						}
						r := snapshot
						rr.req.Body = xio.NopCloser(&r)
					} else {
						rr.req.Body = xio.NopCloser(v)
					}
				} else {
					if !rr.cfg.getBodySet {
						rr.req.GetBody = getNoBody
					}
					rr.req.Body = http.NoBody
				}
			case *strings.Reader:
				rr.cfgBodyClosed = true

				rr.req.ContentLength = int64(v.Len())
				if rr.req.ContentLength != 0 {
					if !rr.cfg.getBodySet {
						snapshot := *v
						rr.req.GetBody = func() (io.ReadCloser, error) {
							{
								b := rr.req.Body
								rr.req.Body = nil
								if b != nil {
									xi_io.ReleaseNopCloser(b)
								}
							}

							r := snapshot
							return xio.NopCloser(&r), nil
						}
						r := snapshot
						rr.req.Body = xio.NopCloser(&r)
					} else {
						rr.req.Body = xio.NopCloser(v)
					}
				} else {
					if !rr.cfg.getBodySet {
						rr.req.GetBody = getNoBody
					}
					rr.req.Body = http.NoBody
				}
			default:
				if rr.cfg.retryEnabled && !rr.cfg.getBodySet {
					var buf buffer
					if bp := rr.cfg.reqBodBufPool; bp != nil {
						v := bp.GetRequestBodyBuffer(rr.req)
						rr.reqBodyBufReleaser = v
						buf = v
					} else {
						buf = &bytes.Buffer{}
					}
					if _, err := io.Copy(buf, bodyReader); err != nil {
						err := fmt.Errorf("failed to buffer request body for retries: %w", err)
						xspan.RecordError(ctx, err, "")
						return err
					}

					// close the config sourced body
					rr.cfgBodyClosed = true
					if rc, ok := bodyReader.(io.ReadCloser); ok {
						if err := rc.Close(); err != nil {
							err := fmt.Errorf("failed to close request body for retries: %w", err)
							xspan.RecordError(ctx, err, "")
							return err
						}
					}

					rr.req.ContentLength = int64(buf.Len())
					if rr.req.ContentLength != 0 {
						r := bytes.NewReader(buf.Bytes())
						rr.req.Body = xio.NopCloser(r)
						rr.req.GetBody = func() (io.ReadCloser, error) {
							{
								b := rr.req.Body
								rr.req.Body = nil
								if b != nil {
									xi_io.ReleaseNopCloser(b)
								}
							}

							r := bytes.NewReader(buf.Bytes())
							return xio.NopCloser(r), nil
						}
					} else {
						rr.reqBodyBufReleaser = nil
						rr.req.GetBody = getNoBody
						rr.req.Body = http.NoBody
					}
				} else if rc, ok := bodyReader.(io.ReadCloser); ok {
					rr.cfgBodyClosed = true
					rr.req.Body = rc
				} else {
					rr.cfgBodyClosed = true
					rr.req.Body = xio.NopCloser(bodyReader)
				}
			}
		} else {
			rr.cfgBodyClosed = true
			if !rr.cfg.getBodySet {
				rr.req.GetBody = getNoBody
			}
		}
	default:
		// should always be unreachable
		panic("unrecognized render type")
	}

	return nil
}

func (rr *reqRunner) buildRequest(ctx context.Context) error {

	rr.req = rr.c.newReq(rr.cfg.method, rr.cfg.path)
	if rr.req.URL.RawQuery != "" || rr.req.URL.RawFragment != "" {
		return errBadPath
	}

	for k, v := range rr.cfg.header {
		rr.req.Header[textproto.CanonicalMIMEHeaderKey(k)] = v
	}

	if otherQ := rr.cfg.query; len(otherQ) > 0 {
		q := rr.req.URL.Query()

		for k, v := range otherQ {
			q[k] = v
		}

		rr.req.URL.RawQuery = q.Encode()
	}

	if err := rr.prepareReqBody(ctx); err != nil {
		return err
	}

	if rr.cfg.userAgent != "" {
		rr.req.Header[headerKeyUserAgent] = []string{rr.cfg.userAgent}
	}

	if rr.cfg.unmarshalJsonTo != nil {
		const key = "Accept"
		oldVals := rr.req.Header[key]
		if size := len(oldVals); size == 0 {
			rr.req.Header[key] = []string{headerValueContentTypeJson}
		} else {
			newVals := make([]string, 0, size+1)
			newVals = append(newVals, oldVals...)
			newVals = append(newVals, headerValueContentTypeJson)
			rr.req.Header[key] = newVals
		}
	}

	rr.req = addTrace(ctx, rr.req)

	if rr.cfg.authAdder != nil {
		if onChecker, ok := rr.cfg.authAdder.(interface{ IsHttpReqAuthRefresherEnabled() bool }); !ok || onChecker.IsHttpReqAuthRefresherEnabled() {
			rr.authRefresher = rr.cfg.authAdder
		}
	}

	rr.authAdder = rr.cfg.authAdder
	if rr.authAdder != nil {
		if staticStratCheck, ok := rr.authAdder.(AuthAdderStrategyDescriber); ok && staticStratCheck.IsAddAuthToHttpRequestStrategyStatic() {
			authedReq, err := rr.authAdder.AddAuthToHttpRequest(rr.req)
			if err != nil {
				return errors.Join(ErrStaticAuthFailed, err)
			}

			rr.req = authedReq
			rr.authAdder = nil
		}
	}
	rr.canCheckAndRefreshAuth = (rr.authRefresher != nil && rr.authAdder != nil)

	return nil
}

func (rr *reqRunner) setProtocolResponseSetter() {
	if rr.cfg.unmarshalJsonTo != nil && rr.cfg.setProtocolResponse {
		if v, ok := rr.cfg.unmarshalJsonTo.(interface{ SetProtocolResponse(*ClientResponse) }); ok {
			rr.prsWantsClientResp = true
			rr.protocolResponseSetter = v
		} else if v, ok := rr.cfg.unmarshalJsonTo.(interface{ SetProtocolResponse(*http.Response) }); ok {
			rr.protocolResponseSetter = v
		} else {
			return
		}
	}
}

func (rr *reqRunner) callProtocolResponseSetter() {
	if rr.teardownSpan == nil {
		rr.forceStartTeardownSpan()
	}

	if rr.protocolResponseSetter == nil {
		return
	}

	switch v := rr.protocolResponseSetter.(type) {
	case interface{ SetProtocolResponse(*ClientResponse) }:
		v.SetProtocolResponse(rr.resp.cr)
	case interface{ SetProtocolResponse(*http.Response) }:
		v.SetProtocolResponse(rr.resp.r)
	default:
		panic("unexpected type in callProtocolResponseSetter")
	}
}

func (rr *reqRunner) preflightCheck(ctx context.Context) error {
	if err := rr.cfg.validate(); err != nil {
		xspan.RecordError(ctx, err, "invalid client request config")
		return err
	}

	if err := xcontext.Cause(ctx); err != nil {
		xspan.RecordError(ctx, err, "context canceled")
		return err
	}

	return nil
}

func (rr *reqRunner) isIdempotentReq() bool {
	if rr.reqIsIdempotentFlagSet {
		return rr.reqIsIdempotentFlag
	}

	if isIdempotentMethod(rr.req.Method) {
		rr.reqIsIdempotentFlag = true
		rr.reqIsIdempotentFlagSet = true
		return true
	}

	if hasIdempotencyHeaderKey(rr.req.Header) {
		rr.reqIsIdempotentFlag = true
		rr.reqIsIdempotentFlagSet = true
		return true
	}

	rr.reqIsIdempotentFlagSet = true
	return false
}

func (rr *reqRunner) retryReq() bool {
	rr.refreshAuth = false

	if rr.errAuthPreDo != nil && rr.authRefresher != nil {
		rr.refreshAuth = true
		return true
	}

	if err := rr.errDo; err != nil && canRetryConnErr(err) {
		return true
	}

	if delay, ok := canRetryAfter(rr.resp.r); ok {
		rr.retryAfter = delay
		rr.retryAfterOK = true
		return true
	}

	if rr.canCheckAndRefreshAuth {
		if err := rr.errDo; rr.authRefresher.CheckIsAuthRejectedHttpResp(rr.resp.r, err) {
			rr.refreshAuth = true
			return true
		}
	}

	onlyBodyReadErr := (!rr.hasErrStatusCode && rr.errReadBody != nil && respStatusSuccess(rr.resp.r))
	canRetryIfIdempotent := (onlyBodyReadErr || canRetryStatusCode(rr.resp.r))

	if canRetryIfIdempotent && rr.isIdempotentReq() {
		return true
	}

	return false
}

func (rr *reqRunner) doWithRetries(ctx context.Context, reqSpanName string) {
	firstCallAt := time.Now()
	rr.doOnceWithRetryPossible(ctx, reqSpanName, firstCallAt, 0)

	retryBaseDelay := rr.cfg.retryBaseDelay
	deadline := firstCallAt.Add(rr.cfg.retryMaxTimeout)

	var failedTryCount int
	var ticker *time.Ticker
	for rr.retryReq() {
		failedTryCount++

		if !rr.respBodyClosed {
			rr.respBodyClosed = true
			bufferAndCloseBody(ctx, rr.cfg.respBodBufPool, rr.resp.r, "potentially retrying")
		}

		var delay time.Duration
		if !rr.retryAfterOK {
			// since go 1.20 math.rand does not need to be seeded.
			// this is not for security purposes so no need to use crypto/rand
			// ref: https://github.com/golang/go/issues/54880
			jitter := int(retryBaseDelay / 10)
			if jitter > 0 {
				jitter = rand.Intn(jitter)
			}
			delay = retryBaseDelay + time.Duration(jitter)
			retryBaseDelay *= 2
			if retryBaseDelay > rr.cfg.retryMaxDelay {
				retryBaseDelay = rr.cfg.retryMaxDelay
			}
		} else {
			delay = rr.retryAfter
			rr.retryAfterOK = false
		}

		if !time.Now().Add(delay).Before(deadline) {
			rr.errRetry = &errRetryDeadlineExceeded{rr.cfg.retryMaxTimeout}
			rr.reqSpan.SetStatus(codes.Error, "retry deadline exceeded")
			rr.reqSpanStatSet = true
			return
		}

		//
		// sleep for the delay duration or until the context is done
		//

		if ticker == nil {
			ticker = time.NewTicker(delay)
		} else {
			ticker.Reset(delay)
		}
		select {
		case <-ticker.C:
			ticker.Stop()

			// next select ensures the ticker is drained
			// should the timeout be very small
			// and a value written before it could be stopped

			// note: no need for a for loop here as .Stop() ensures that the ticker will not
			// write to the channel again and since the channel buffer size is 1 there could
			// only be one value written to the channel by the time stop returns.

			select {
			case <-ticker.C:
			default:
			}
		case <-ctx.Done():
			ticker.Stop()

			rr.errRetry = xcontext.Cause(ctx)
			rr.reqSpan.SetStatus(codes.Error, "parent context canceled before retry deadline")
			rr.reqSpanStatSet = true
			return
		}

		// prepare new body reader
		if rr.errAuthPreDo == nil {
			// note that this is intentionally not done before sleeping the delay time
			// not only because then we don't need to recompute the delay time, but because
			// the implementation of GetBody could vary wildly and the body returned could have
			// an open/read deadline.
			//
			// Going to treat it as essentially part of the request phase but not make the
			// request appear to start until do is really called.
			//
			// Authors are encouraged to keep GetBody as simple as possible and let io
			// read & write complexities (if they have them) occur concurrently as much
			// as possible.
			b, berr := rr.req.GetBody()
			if berr != nil {
				rr.errRetry = &errBadGetBodyResp{berr}
				xspan.RecordError(ctx, rr.errRetry, "failed to get a new request body for a retry")
				rr.reqSpan.SetStatus(codes.Error, "failed to get a new request body for a retry")
				rr.reqSpanStatSet = true
				return
			}

			rr.req.Body = b
			rr.reqBodyClosed = false
		}

		rr.doOnceWithRetryPossible(ctx, reqSpanName, time.Now(), failedTryCount)
	}
}

func (rr *reqRunner) doOnceWithRetryPossible(ctx context.Context, reqSpanName string, now time.Time, failedTryCount int) {
	ctx, cancel := context.WithDeadline(ctx, now.Add(rr.cfg.perCallTimeout))
	defer cancel()

	rr.errAuthPreDo = nil
	rr.errDo = nil
	rr.hasErrStatusCode = false
	rr.errReadBody = nil
	rr.errUnmarshal = nil
	// note, the retry error remains unchanged because when it becomes non-nil a retry is not possible

	if rr.refreshAuth {
		if err := xcontext.Cause(ctx); err != nil {
			rr.respBodyClosed = true
			rr.resp.r = nil
			// TODO: wrap err in additional detail around deadline?
			xspan.RecordError(ctx, err, "context canceled")

			rr.errDo = err
			return
		}

		if err := rr.authRefresher.RefreshHttpAuth(ctx); err != nil {
			rr.respBodyClosed = true
			rr.resp.r = nil
			xspan.RecordError(ctx, err, "failed to refresh auth")

			rr.errDo = err
			return
		}
	}

	nextReq := rr.req
	if rr.authAdder != nil {
		authedReq, err := rr.authAdder.AddAuthToHttpRequest(nextReq)
		if err != nil {
			rr.errAuthPreDo = errors.Join(ErrAddAuthFailed, err)
		} else {
			nextReq = authedReq
		}
	}

	spanOptsBuf := [...]trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindClient),
		nil,
		nil,
	}
	spanOpts := spanOptsBuf[:1]
	if rr.c.cfg.serviceName != "" {
		spanOpts = append(spanOpts, trace.WithAttributes(semconv.PeerService(rr.c.cfg.serviceName)))
	}

	// start a new span for the request
	//
	// TODO: instead of using the host url component alone, we
	// might be better served by using the host and url path
	// template.
	if failedTryCount > 0 {
		spanOpts = append(spanOpts,
			trace.WithAttributes(
				attribute.KeyValue{Key: "http.request.client.resend_count", Value: attribute.IntValue(failedTryCount)},
			),
		)
	}
	ctx, span := otel.Tracer("").Start(ctx, reqSpanName, spanOpts...)
	var spanEnded bool
	defer func() {
		if spanEnded {
			return
		}

		span.SetStatus(codes.Error, "panicking")
		span.End()
	}()

	if err := rr.errAuthPreDo; err != nil {
		rr.respBodyClosed = true
		rr.resp.r = nil

		const errMsg = "preflight auth error"
		xspan.RecordError(ctx, err, errMsg)

		span.SetStatus(codes.Error, errMsg)
		spanEnded = true
		span.End()
		return
	}

	nextReq = nextReq.WithContext(ctx)
	hClient := rr.c.cfg.httpClient

	if err := xcontext.Cause(ctx); err != nil {
		rr.respBodyClosed = true
		rr.resp.r = nil

		const errMsg = "request timeout"
		// TODO: wrap err in additional detail around deadline?
		xspan.RecordError(ctx, err, errMsg)

		rr.errDo = err
		span.SetStatus(codes.Error, errMsg)
		spanEnded = true
		span.End()
		return
	}

	rr.respBodyClosed = false
	rr.reqBodyClosed = true

	rr.resp.r, rr.errDo = hClient.Do(nextReq)
	if rr.errDo != nil {
		rr.resp.r = nil

		const errMsg = "connection or protocol error"
		xspan.RecordError(ctx, rr.errDo, errMsg)
		span.SetStatus(codes.Error, errMsg)
		spanEnded = true
		span.End()
		return
	}
	span.AddEvent("got-response", trace.WithAttributes(
		attribute.Int("http.status_code", rr.resp.r.StatusCode),
	))

	if rr.resp.r.StatusCode >= 500 {
		span.SetStatus(codes.Error, "received server error response")
	} else if rr.resp.r.StatusCode >= 400 {
		span.SetStatus(codes.Error, "received client error response")
	} else if rr.resp.r.StatusCode < 200 {
		// it's an error because it's totally unexpected for a req-resp cycle
		span.SetStatus(codes.Error, "received informational response")
	} else {
		span.SetStatus(codes.Ok, "")
	}

	spanEnded = true
	span.End()

	// post request processing

	if !rr.firstErrRespSet || rr.firstErrResp.errDo != nil {

		//
		// If there is no first error response tracker set
		// Or if the first error response was really a failure to complete the
		// round trip minus the body
		//

		defer func() {
			if rr.errDo == nil && !rr.hasErrStatusCode && rr.errReadBody == nil && rr.errUnmarshal == nil {
				return
			}

			rr.firstErrResp = firstErrResponse{
				r:                rr.resp.r,
				errDo:            rr.errDo,
				hasErrStatusCode: rr.hasErrStatusCode,
				errReadBody:      rr.errReadBody,
				errUnmarshal:     rr.errUnmarshal,
			}
			rr.firstErrRespSet = true
		}()
	}

	if rr.resp.r.StatusCode < 200 || rr.resp.r.StatusCode >= 400 {
		// TODO: support client or do options declaring a strategy for acceptable status code values
		// as this breadth is likely too wide
		//
		// what should be done if different response codes / headers require different unmarshaling targets/types?
		//
		// caller can always use a
		//
		// type jsonResp struct {
		// 	json.RawMessage
		// 	service.TransportResp[service.HttpResponse]
		// }
		// &jsonResp{}
		//
		// as a json body target
		err := &errRespStatusCodeNotSuccess{rr.resp.r.StatusCode}
		rr.hasErrStatusCode = true
		xspan.RecordError(ctx, nil, err.Error())
	}

	if !rr.autoCloseRespBody && rr.cfg.unmarshalJsonTo == nil {
		// not auto-closing response body and not further processing a json response
		return
	}

	rr.respBodyClosed = true
	buf := bufferAndCloseBody(ctx, rr.cfg.respBodBufPool, rr.resp.r, "buffering potential success")

	if rr.errDo != nil || rr.cfg.unmarshalJsonTo == nil {
		return
	}

	// no error with the round trip, and we need to decode the json body

	if v, ok := rr.resp.r.Body.(erroredNopReader); ok {
		err := v.err
		rr.errReadBody = err
		xspan.RecordError(ctx, err, "failed to fully read json response body")
		return
	}

	if ct := rr.resp.r.Header.Get(headerKeyContentType); ct != headerValueContentTypeJson && !strings.HasPrefix(ct, headerValueContentTypeJson+";") {
		err := ErrNotJSONResp
		rr.errUnmarshal = err
		xspan.RecordError(ctx, err, "")
		return
	}

	if buf == nil || buf.Len() == 0 {
		err := ErrNoBodyInJSONResp
		rr.errUnmarshal = err
		xspan.RecordError(ctx, err, "")
		return
	}

	if err := json.Unmarshal(buf.Bytes(), rr.cfg.unmarshalJsonTo); err != nil {
		rr.errUnmarshal = err
		xspan.RecordError(ctx, err, "")
	}
}

func (rr *reqRunner) doOnce(ctx context.Context, reqSpanName string, now time.Time) {
	ctx, cancel := context.WithDeadline(ctx, now.Add(rr.cfg.perCallTimeout))
	defer cancel()

	rr.errAuthPreDo = nil
	rr.errDo = nil
	rr.hasErrStatusCode = false
	rr.errReadBody = nil
	rr.errUnmarshal = nil
	// note, the retry error remains unchanged because when it becomes non-nil a retry is not possible

	if rr.refreshAuth {
		if err := xcontext.Cause(ctx); err != nil {
			rr.respBodyClosed = true
			rr.resp.r = nil
			// TODO: wrap err in additional detail around deadline?
			xspan.RecordError(ctx, err, "context canceled")

			rr.errDo = err
			return
		}

		if err := rr.authRefresher.RefreshHttpAuth(ctx); err != nil {
			rr.respBodyClosed = true
			rr.resp.r = nil
			xspan.RecordError(ctx, err, "failed to refresh auth")

			rr.errDo = err
			return
		}
	}

	nextReq := rr.req
	if rr.authAdder != nil {
		authedReq, err := rr.authAdder.AddAuthToHttpRequest(nextReq)
		if err != nil {
			rr.errAuthPreDo = errors.Join(ErrAddAuthFailed, err)
		} else {
			nextReq = authedReq
		}
	}

	spanOptsBuf := [...]trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindClient),
		nil,
	}
	spanOpts := spanOptsBuf[:1]
	if rr.c.cfg.serviceName != "" {
		spanOpts = append(spanOpts, trace.WithAttributes(semconv.PeerService(rr.c.cfg.serviceName)))
	}

	ctx, span := otel.Tracer("").Start(ctx, reqSpanName, spanOpts...)
	var spanEnded bool
	defer func() {
		if spanEnded {
			return
		}

		span.SetStatus(codes.Error, "panicking")
		span.End()
	}()

	if err := rr.errAuthPreDo; err != nil {
		const errMsg = "preflight auth error"
		xspan.RecordError(ctx, err, errMsg)
		span.SetStatus(codes.Error, errMsg)
		spanEnded = true
		span.End()
		return
	}

	nextReq = nextReq.WithContext(ctx)
	hClient := rr.c.cfg.httpClient

	if err := xcontext.Cause(ctx); err != nil {
		rr.respBodyClosed = true
		rr.resp.r = nil
		const errMsg = "request timeout"
		// TODO: wrap err in additional detail around deadline?
		xspan.RecordError(ctx, err, errMsg)
		span.SetStatus(codes.Error, errMsg)
		spanEnded = true
		span.End()

		rr.errDo = err
		return
	}

	rr.respBodyClosed = false
	rr.reqBodyClosed = true

	newResp, err := hClient.Do(nextReq)
	if err != nil {
		xspan.RecordError(ctx, err, "connection or protocol error")
		span.SetStatus(codes.Error, "connection or protocol error")
		spanEnded = true
		span.End()

		rr.errDo = err
		return
	}
	span.AddEvent("got-response", trace.WithAttributes(
		attribute.Int("http.status_code", newResp.StatusCode),
	))

	if newResp.StatusCode >= 500 {
		span.SetStatus(codes.Error, "received server error response")
	} else if newResp.StatusCode >= 400 {
		span.SetStatus(codes.Error, "received client error response")
	} else if newResp.StatusCode < 200 {
		// it's an error because it's totally unexpected for a req-resp cycle
		span.SetStatus(codes.Error, "received informational response")
	} else {
		span.SetStatus(codes.Ok, "")
	}

	spanEnded = true
	span.End()

	rr.resp.r = newResp

	// post request processing

	if rr.resp.r.StatusCode < 200 || rr.resp.r.StatusCode >= 400 {
		// TODO: support client or do options declaring a strategy for acceptable status code values
		// as this breadth is likely too wide
		//
		// what should be done if different response codes / headers require different unmarshaling targets/types?
		//
		// caller can always use a
		//
		// type jsonResp struct {
		// 	json.RawMessage
		// 	service.TransportResp[service.HttpResponse]
		// }
		// &jsonResp{}
		//
		// as a json body target
		err := &errRespStatusCodeNotSuccess{rr.resp.r.StatusCode}
		rr.hasErrStatusCode = true
		xspan.RecordError(ctx, nil, err.Error())
	}

	if !rr.autoCloseRespBody && rr.cfg.unmarshalJsonTo == nil {
		// not auto-closing response body and not further processing a json response
		return
	}

	rr.respBodyClosed = true
	buf := bufferAndCloseBody(ctx, rr.cfg.respBodBufPool, rr.resp.r, "buffering potential success")

	if rr.errDo != nil || rr.cfg.unmarshalJsonTo == nil {
		return
	}

	// no error with the round trip, and we need to decode the json body

	if v, ok := rr.resp.r.Body.(erroredNopReader); ok {
		err := v.err
		rr.errReadBody = err
		xspan.RecordError(ctx, err, "failed to fully read json response body")
		return
	}

	if ct := rr.resp.r.Header.Get(headerKeyContentType); ct != headerValueContentTypeJson && !strings.HasPrefix(ct, headerValueContentTypeJson+";") {
		err := ErrNotJSONResp
		rr.errUnmarshal = err
		xspan.RecordError(ctx, err, "")
		return
	}

	if buf == nil || buf.Len() == 0 {
		err := ErrNoBodyInJSONResp
		rr.errUnmarshal = err
		xspan.RecordError(ctx, err, "")
		return
	}

	if err := json.Unmarshal(buf.Bytes(), rr.cfg.unmarshalJsonTo); err != nil {
		rr.errUnmarshal = err
		xspan.RecordError(ctx, err, "")
	}
}

func (rr *reqRunner) endTeardownSpan() {
	if span := rr.teardownSpan; span != nil {
		rr.teardownSpan = nil

		span.End()
	}
}

func (rr *reqRunner) closeCfgBody() {
	if rr.teardownSpan == nil {
		rr.forceStartTeardownSpan()
	}

	if rr.cfgBodyClosed {
		return
	}

	rc, ok := rr.cfg.body.(io.ReadCloser)
	if !ok {
		return
	}

	err := rc.Close()
	if err == nil {
		return
	}

	rr.teardownErrs[rr.numTeardownErrs] = err
	rr.numTeardownErrs++
}

func (rr *reqRunner) closeReqBody() {
	if rr.teardownSpan == nil {
		rr.forceStartTeardownSpan()
	}

	if rr.reqBodyClosed || rr.req == nil || rr.req.Body == nil {
		return
	}

	err := rr.req.Body.Close()
	if err == nil {
		return
	}

	rr.teardownErrs[rr.numTeardownErrs] = err
	rr.numTeardownErrs++
}

func (rr *reqRunner) endSetupSpanOK() {
	span := rr.setupSpan
	rr.setupSpan = nil

	span.SetStatus(codes.Ok, "")
	span.End()
}

func (rr *reqRunner) startTeardownSpan() {
	if rr.teardownSpan != nil {
		return
	}

	// was not already started
	// (note this can be called up to 2 times)
	_, rr.teardownSpan = otel.Tracer("").Start(rr.lifecycleContext, "xhttp.request.teardown")
}

// forceStartTeardownSpan ensures that the teardown phase has started properly
// while other phases have been properly terminated.
//
// This function is only called under circumstances that are not a normal exit
// case of the reqRunner.run function as part of defer function gates.
//
// The caller should only call if `rr.teardownSpan == nil`
// as a mechanical gate that is inlined to avoid the stack
// overhead of a function call that would just return.
func (rr *reqRunner) forceStartTeardownSpan() {

	// end setup span with status = error if not already finalized
	//
	// otherwise // end request span with appropriate status if not already finalized
	if span := rr.setupSpan; span != nil {
		rr.setupSpan = nil

		span.SetStatus(codes.Error, "setup failed")
		span.End()
	} else if span := rr.reqSpan; span != nil {
		rr.reqSpan = nil

		if !rr.reqSpanStatSet {
			if !rr.endOfReqSpanReached {
				span.SetStatus(codes.Error, "panicking")
			} else if rr.errDo == nil && !rr.hasErrStatusCode && rr.errReadBody == nil && rr.errUnmarshal == nil && rr.errRetry == nil {
				span.SetStatus(codes.Ok, "")
			} else {
				span.SetStatus(codes.Error, "request strategy failed")
			}
		}

		span.End()
	}

	rr.startTeardownSpan()
}

func (rr *reqRunner) finalize(rPtr **http.Response, crPtr **ClientResponse, errPtr *error) {
	if v := rr.reqBodyBufReleaser; v != nil {
		rr.reqBodyBufReleaser = nil

		if r := rr.req; r != nil {
			if b := r.Body; b != nil {
				r.Body = nil

				xi_io.ReleaseNopCloser(b)
			}

			r.GetBody = nil
		}

		if r := rr.resp.r; r != nil {
			if r := r.Request; r != nil {
				if b := r.Body; b != nil {
					r.Body = nil

					xi_io.ReleaseNopCloser(b)
				}

				r.GetBody = nil
			}
		}

		v.Release()
	}

	if *errPtr != nil {

		// must have failed early in the setup
		// rPtr and crPtr are guaranteed to be nil
		// as are the rr.resp internal values

		if n := rr.numTeardownErrs; n > 0 {
			copy(rr.teardownErrs[1:1+n], rr.teardownErrs[:n])
			rr.teardownErrs[0] = *errPtr

			n++

			*errPtr = errors.Join(rr.teardownErrs[:n]...)
		}

		return
	}

	if !rr.autoCloseRespBody {
		*rPtr = rr.resp.r
	} else if cr := rr.resp.cr; cr != nil {
		// client response is not guaranteed to always be non-nil
		// in the case of DNS failures or connect failures
		*crPtr = cr
		if r := cr.Request; r != nil {
			r.Body = nil
		}
	}

	var prevErrCount uint8

	if rr.errAuthPreDo != nil {
		prevErrCount++
	}

	if rr.errDo != nil {
		prevErrCount++
	}

	if rr.hasErrStatusCode {
		prevErrCount++
	}

	if rr.errReadBody != nil {
		prevErrCount++
	}

	if rr.errUnmarshal != nil {
		prevErrCount++
	}

	if prevErrCount == 0 && rr.numTeardownErrs == 0 {
		return
	}

	copy(rr.teardownErrs[prevErrCount:prevErrCount+rr.numTeardownErrs], rr.teardownErrs[:rr.numTeardownErrs])
	rr.numTeardownErrs += prevErrCount

	var i uint8
	if err := rr.errAuthPreDo; err != nil {
		rr.teardownErrs[i] = err
		i++
	}
	if err := rr.errDo; err != nil {
		rr.teardownErrs[i] = err
		i++
	}
	if rr.hasErrStatusCode {
		rr.teardownErrs[i] = &errRespStatusCodeNotSuccess{rr.resp.r.StatusCode}
		i++
	}
	if err := rr.errReadBody; err != nil {
		rr.teardownErrs[i] = err
		i++
	}
	if err := rr.errUnmarshal; err != nil {
		rr.teardownErrs[i] = err
	}
	*errPtr = errors.Join(rr.teardownErrs[:rr.numTeardownErrs]...)
}
