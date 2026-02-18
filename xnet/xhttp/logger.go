package xhttp

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ballastworks/xs/xlog/xslog"
	"github.com/ballastworks/xs/xsync/xrwm"
)

const (
	reqLoggerElapsedTimeKey = "x.http.elapsed_time"
)

var (
	ErrBadRequestLoggerFactoryConfig = errors.New("bad request logger factory config")
)

// log semantics by domain: https://opentelemetry.io/docs/specs/semconv/registry/attributes/

func xffStr(values []string) (string, bool) {
	var buf []byte

	for _, v := range values {
		numBytes := len(v)

		for numBytes > 0 {
			k := strings.LastIndexByte(v[:numBytes], ',')

			var ip string
			if k < 0 {
				ip = v[:numBytes]
				numBytes = 0
			} else {
				ip = v[k+1 : numBytes]
				numBytes = k
			}

			ip = strings.TrimSpace(ip)
			if ip == "" {
				continue
			}

			if len(buf) > 0 {
				buf = append(buf, ", "...)
			}

			buf = append(buf, ip...)
		}
	}

	return string(buf), (len(values) > 0)
}

func forwardedStr(values []string) (string, bool) {
	if len(values) == 1 {
		if strings.TrimSpace(values[0]) == "" {
			return "", true
		}
		return values[0], true
	}

	var buf []byte

	for _, v := range values {
		v := strings.TrimSpace(v)
		if v == "" {
			continue
		}

		if len(buf) > 0 {
			buf = append(buf, ", "...)
		}

		buf = append(buf, v...)
	}

	return string(buf), (len(values) > 0)
}

func privateRemoteClientAddr(values []string, remoteAddrPort string) (string, uint16, bool) {

	// Scan from the rightmost value (nearest proxy) to leftmost (original client).
	var firstHopAddr netip.Addr
	var lastOKPrivateAddr string
	var lastOKPrivatePort uint16

	if v, err := netip.ParseAddrPort(remoteAddrPort); err != nil {
		return "", 0, false
	} else {
		a := v.Addr()

		if !(a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsUnspecified() || a.IsPrivate()) {
			return "", 0, false
		}

		firstHopAddr = a
		lastOKPrivatePort = v.Port()
	}
	lastOKPrivatePortValid := true

	for i := len(values) - 1; i >= 0; i-- {
		s := values[i]
		numBytes := len(s)

		for numBytes > 0 {
			k := strings.LastIndexByte(s[:numBytes], ',')
			var addrStr string
			var port uint16
			var portValid bool
			if k < 0 {
				addrStr = s[:numBytes]
				numBytes = 0
			} else {
				addrStr = s[k+1 : numBytes]
				numBytes = k
			}

			addrStr = strings.TrimSpace(addrStr)
			if addrStr == "" {
				continue
			}

			var addr netip.Addr
			if v, err := netip.ParseAddr(addrStr); err == nil {
				addr = v
				port = 0
				portValid = false
			} else if v, err := netip.ParseAddrPort(addrStr); err == nil {
				addr = v.Addr()
				port = v.Port()
				portValid = true
			} else {
				goto RETURN_LAST_HIT
			}

			if addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsUnspecified() {
				continue
			}

			if !addr.IsPrivate() {
				goto RETURN_LAST_HIT
			}

			lastOKPrivateAddr = addr.String()
			lastOKPrivatePort = port
			lastOKPrivatePortValid = portValid
		}
	}

RETURN_LAST_HIT:

	if lastOKPrivateAddr != "" {
		return lastOKPrivateAddr, lastOKPrivatePort, lastOKPrivatePortValid
	}

	return firstHopAddr.String(), lastOKPrivatePort, lastOKPrivatePortValid
}

type reqDataState struct {
	start time.Time
	req   *http.Request
}

type reqData struct {
	reqLogCacher

	reqDataState
}

func (rd *reqData) init() {
	rd.start = time.Now()
}

func (rd *reqData) reset() {
	rd.reqLogCacher.reset()
	rd.reqDataState = reqDataState{}
}

func (rd *reqData) resolveWithCachedAttrs(ctx context.Context,
	logf xslog.LoggerFactory,
	maxAttrCount int,
	attrsResolvers ...attrsResolver,
) xslog.Logger {
	rd.Lock()
	st := xrwm.WLocked
	defer func() {
		if st == xrwm.WLocked {
			rd.Unlock()
		}
	}()

	var attrs []slog.Attr
	if v := rd.state.Load().(reqLogCacheState).attrs; v != nil {
		attrs = v
		goto RETURN_LOGGER
	}

	attrs = make([]slog.Attr, 0, maxAttrCount)

	for _, f := range attrsResolvers {
		f(rd.start, rd.req, &attrs)
	}

	rd.state.Store(reqLogCacheState{nil, attrs[:len(attrs):len(attrs)]})

	rd.Unlock()
	st = xrwm.Unlocked

RETURN_LOGGER:
	attrs = append(attrs, slog.Duration(reqLoggerElapsedTimeKey, time.Since(rd.start)))
	return logf.Logger(ctx).WithAttrs(ctx, attrs...)
}

func (rd *reqData) resolveWithCachedLogger(ctx context.Context,
	logf xslog.LoggerFactory,
	maxAttrCount int,
	attrsResolvers ...attrsResolver,
) xslog.Logger {
	rd.Lock()
	st := xrwm.WLocked // moved to top of the function - must be declared before first GOTO statement
	defer func() {
		if st == xrwm.WLocked {
			rd.Unlock()
		}
	}()

	var logger xslog.Logger
	var attrs []slog.Attr
	if v := rd.state.Load().(reqLogCacheState).logger; v != nil {
		logger = v
		goto RETURN_LOGGER
	}

	// intentionally waiting to convert to UTC until here to reduce unnecessary work
	if rd.start.Location() != time.UTC {
		rd.start = rd.start.UTC()
	}

	attrs = make([]slog.Attr, 0, maxAttrCount)
	for _, f := range attrsResolvers {
		f(rd.start, rd.req, &attrs)
	}

	// clipping the slice just to avoid append shenanigans I've yet to predict
	//
	// Not using slices.Clip just because I want to keep it as hot as possible.
	attrs = attrs[:len(attrs):len(attrs)]

	logger = logf.Logger(ctx).WithAttrs(ctx, attrs...)

	rd.state.Store(reqLogCacheState{logger, nil})

	rd.Unlock()
	st = xrwm.Unlocked

RETURN_LOGGER:
	return logger.WithAttrs(ctx, slog.Duration(reqLoggerElapsedTimeKey, time.Since(rd.start)))
}

type reqDataCtxKey struct{}

type requestLoggerFactoryConfig struct {
	logf        xslog.LoggerFactory
	logfSet     bool
	cacheLogger bool
}
type RequestLoggerFactoryOption func(*requestLoggerFactoryConfig)
type requestLoggerFactoryOpts struct{}

func RequestLoggerFactoryOpts() requestLoggerFactoryOpts {
	return requestLoggerFactoryOpts{}
}

func (requestLoggerFactoryOpts) LoggerFactory(factory xslog.LoggerFactory) RequestLoggerFactoryOption {
	return func(cfg *requestLoggerFactoryConfig) {
		cfg.logf = factory
		cfg.logfSet = true
	}
}

func (requestLoggerFactoryOpts) CacheLogger(b bool) RequestLoggerFactoryOption {
	return func(cfg *requestLoggerFactoryConfig) {
		cfg.cacheLogger = b
	}
}

// TODO: move to correlationLogger MiddlewareOptions
// recordPotentialGatewayPII bool
// recordQueryStringDetails  bool

// func (requestLoggerFactoryOpts) RecordQueryStringDetails(b bool) RequestLoggerFactoryOption {
// 	return func(cfg *requestLoggerFactoryConfig) {
// 		cfg.recordQueryStringDetails = b
// 	}
// }

// func (requestLoggerFactoryOpts) RecordPotentialGatewayPII(b bool) RequestLoggerFactoryOption {
// 	return func(cfg *requestLoggerFactoryConfig) {
// 		cfg.recordPotentialGatewayPII = b
// 	}
// }

func (cfg *requestLoggerFactoryConfig) validate() error {
	if cfg.logf == nil {
		if cfg.logfSet {
			return errors.New("nil logger factory specified")
		}

		cfg.logf = defaultLoggerFactoryFoundation
	}

	return nil
}

var _ xslog.LoggerFactory = &xhttpLoggerFactory{} // TODO: move to test

func NewRequestLoggerFactory(options ...RequestLoggerFactoryOption) (*xhttpLoggerFactory, error) {
	cfg := requestLoggerFactoryConfig{
		// empty
	}

	for _, f := range options {
		f(&cfg)
	}

	if err := cfg.validate(); err != nil {
		return nil, errors.Join(ErrBadRequestLoggerFactoryConfig, err)
	}

	var attrsResolvers []attrsResolver
	var maxAttrCount int
	{
		const reqStartTimeRelativeAttrCount = 1

		resolverPlan := baseAttrs()

		nResolvers, nAttrs := resolverPlan(nil)

		resolvers := make([]attrsResolver, 0, nResolvers)
		resolverPlan(&resolvers)

		attrsResolvers = resolvers
		maxAttrCount = nAttrs + reqStartTimeRelativeAttrCount
	}

	v := &xhttpLoggerFactory{xhttpLoggerInternalFactory{cfg.logf, attrsResolvers, maxAttrCount, cfg.cacheLogger}, nil}
	v.w = xslog.NewLoggerWrappingFactory(&v.xhttpLoggerInternalFactory)
	return v, nil
}

type attrsResolver = func(start time.Time, req *http.Request, attrs *[]slog.Attr)

type attrsResolverPlan = func(attrResolvers *[]attrsResolver) (int, int)

func lowCardinalityRoute(req *http.Request) string {

	// TODO: consider adding path value key-value pairs to the trace and even sourcing instead from the root req trace

	if p := req.Pattern; p != "" {
		return p
	}

	return req.URL.Path
}

type perLogClientMeta struct {
	service       string
	namespace     string
	vcsRepository string
}

// type reqEndLogClientMeta struct {
// 	version     string
// 	instanceID  string
// 	vcsRevision string
// }

var (
	cmVcsRe = regexp.MustCompile(`(?:^|;)\s*vcs\s*=\s*[^;]*\s*(?:;|$)`)
	// cmRevRe = regexp.MustCompile(`(?:^|;)\s*rev\s*=\s*[^;]*\s*(?:;|$)`)
	cmNsRe = regexp.MustCompile(`(?:^|;)\s*ns\s*=\s*[^;]*\s*(?:;|$)`)
	// cmIdRe  = regexp.MustCompile(`(?:^|;)\s*id\s*=\s*[^;]*\s*(?:;|$)`)
)

func extractClientMetaVal(r *regexp.Regexp, s string) string {
	s = r.FindString(s)
	if s == "" {
		return ""
	}

	_, s, _ = strings.Cut(s, "=")
	s = strings.TrimRight(s, ";")
	return strings.TrimSpace(s)
}

func getPerLogClientMeta(header http.Header) (perLogClientMeta, bool) {
	var result, nv perLogClientMeta

	if vv := header["User-Agent"]; len(vv) > 0 {
		v := strings.TrimSpace(vv[0])
		if i := strings.IndexAny(v, "/(;"); i != -1 {
			v = v[:i]
		}

		nv.service = strings.TrimSpace(v)
	}
	if nv.service == "" {
		// no service name, so short circuiting
		return result, false
	}

	if vv := header["X-Client-Build"]; len(vv) > 0 {
		if v := strings.TrimSpace(vv[0]); v != "" {
			nv.vcsRepository = extractClientMetaVal(cmVcsRe, v)
		}
	}
	if nv.vcsRepository == "" {
		// must have at least the repository information, so short circuiting
		return result, false
	}

	if vv := header["X-Client-Identity"]; len(vv) > 0 {
		if v := strings.TrimSpace(vv[0]); v != "" {
			nv.namespace = extractClientMetaVal(cmNsRe, v)
		}
	}

	result = nv
	return result, true
}

// TODO: uncomment and utilize
// func getReqEndLogClientMeta(header http.Header) (reqEndLogClientMeta, bool) {
// 	var result, nv reqEndLogClientMeta

// 	if vv := header["User-Agent"]; len(vv) > 0 {
// 		v := strings.TrimSpace(vv[0])
// 		if i := strings.IndexAny(v, "(;"); i != -1 {
// 			v = v[:i]
// 		}

// 		svc, v, ok := strings.Cut(strings.TrimSpace(v), "/")
// 		if strings.TrimSpace(svc) == "" {
// 			// no service name, so short circuiting
// 			return result, false
// 		}

// 		if ok {
// 			nv.version = strings.TrimSpace(v)
// 		}
// 	} else {
// 		// no service name, so short circuiting
// 		return result, false
// 	}

// 	if vv := header["X-Client-Build"]; len(vv) > 0 {
// 		if v := strings.TrimSpace(vv[0]); v != "" {
// 			if vcsRepo := extractClientMetaVal(cmVcsRe, v); strings.TrimSpace(vcsRepo) == "" {
// 				// no repository information, so short circuiting
// 				return result, false
// 			}

// 			nv.vcsRevision = extractClientMetaVal(cmRevRe, v)
// 		} else {
// 			// no repository information, so short circuiting
// 			return result, false
// 		}
// 	} else {
// 		// no repository information, so short circuiting
// 		return result, false
// 	}

// 	if vv := header["X-Client-Identity"]; len(vv) > 0 {
// 		if v := strings.TrimSpace(vv[0]); v != "" {
// 			nv.instanceID = extractClientMetaVal(cmIdRe, v)
// 		}
// 	}

// 	result = nv
// 	return result, true
// }

func baseAttrs() attrsResolverPlan {
	// TODO: add these values to parent request span as well

	return func(attrResolvers *[]attrsResolver) (int, int) {
		var resCount, attrCount int

		if attrResolvers != nil {
			*attrResolvers = append(*attrResolvers, func(start time.Time, req *http.Request, attrs *[]slog.Attr) {
				*attrs = append(*attrs,
					slog.String("http.request.method", req.Method),
					slog.String("http.route", lowCardinalityRoute(req)),
					slog.Time("x.http.start", start),
				)
				// 3 / 3

				if ipStr, port, portValid := privateRemoteClientAddr(req.Header["X-Forwarded-For"], req.RemoteAddr); ipStr != "" {
					// TODO: note that gateway PII logs should use a different approach and not trust any aspects of the X-Forwarded-For header (unless X-Forwarded-For-Signature and X-Forwarded-For-Authority are set?).
					*attrs = append(*attrs,
						slog.String("x.client.private.address", ipStr),
					)
					if portValid {
						*attrs = append(*attrs,
							slog.Uint64("x.client.private.port", uint64(port)),
						)
					}
				}
				// 2 / 5

				// KNOW YOUR CLIENT:

				if cm, ok := getPerLogClientMeta(req.Header); ok {
					// TODO: move these to a correlation_log event

					// if cm.version != "" {
					// 	*attrs = append(*attrs,
					// 		slog.String("service.version", cm.version),
					// 	)
					// }

					// if cm.instanceID != "" {
					// 	*attrs = append(*attrs,
					// 		slog.String("service.instance.id", cm.instanceID),
					// 	)
					// }

					// if cm.vcsRevision != "" {
					// 	*attrs = append(*attrs,
					// 		slog.String("x.service.vcs.revision", cm.vcsRevision),
					// 	)
					// }

					*attrs = append(*attrs,
						slog.String("service.name", cm.service),
					)

					if cm.namespace != "" {
						*attrs = append(*attrs,
							slog.String("service.namespace", cm.namespace),
						)
					}

					*attrs = append(*attrs,
						slog.String("x.service.vcs.repository", cm.vcsRepository),
					)
				}
				// 3 / 8
			})
		}
		resCount++
		attrCount += 8

		return resCount, attrCount
	}
}

// TODO: uncomment and utilize
// func potentialGatewayPIIAttrs() attrsResolverPlan {
// 	// TODO: add these values to parent request span as well

// 	// TODO: emit as request_correlation log attributes instead
// 	return func(attrResolvers *[]attrsResolver) (int, int) {
// 		var resCount, attrCount int

// 		if attrResolvers != nil {
// 			*attrResolvers = append(*attrResolvers, func(start time.Time, req *http.Request, attrs *[]slog.Attr) {
// 				if v := req.Referer(); v != "" {
// 					*attrs = append(*attrs,
// 						slog.String("http.request.header.referer", v),
// 					)
// 				}
// 				// 1 / 1

// 				if v, ok := forwardedStr(req.Header["Forwarded"]); ok {
// 					if v == "" {
// 						*attrs = append(*attrs,
// 							slog.Bool("x.http.request.header-meta.forwarded.present-empty", true),
// 						)
// 					} else {
// 						*attrs = append(*attrs,
// 							slog.String("http.request.header.forwarded", v),
// 						)
// 					}
// 				}
// 				// 1 / 2

// 				if v := req.Header.Get("X-Forwarded-Host"); v != "" {
// 					*attrs = append(*attrs,
// 						slog.String("http.request.header.x-forwarded-host", v),
// 					)
// 				}
// 				// 1 / 3

// 				if v, ok := xffStr(req.Header["X-Forwarded-For"]); ok {
// 					if v == "" {
// 						*attrs = append(*attrs,
// 							slog.Bool("x.http.request.header-meta.x-forwarded-for.present-empty", true),
// 						)
// 					} else {
// 						*attrs = append(*attrs,
// 							slog.String("http.request.header.x-forwarded-for", v),
// 						)
// 					}
// 				}
// 				// 1 / 4

// 				// TODO: real_ip?
// 			})
// 		}
// 		resCount++
// 		attrCount += 4

// 		return resCount, attrCount
// 	}
// }

func reqDataFromContext(ctx context.Context) *reqData {
	if v, ok := ctx.Value(reqDataCtxKey{}).(*reqData); ok {
		return v
	}

	return nil
}

type reqLogCacheState struct {
	logger xslog.Logger
	attrs  []slog.Attr
}

type reqLogCacher struct {
	sync.Mutex
	state atomic.Value
}

func newReqLogCacher() reqLogCacher {
	var state atomic.Value
	state.Store(reqLogCacheState{nil, nil})

	return reqLogCacher{state: state}
}

func (rc *reqLogCacher) reset() {
	rc.state.Store(reqLogCacheState{nil, nil})
}

var reqDataPool = sync.Pool{
	New: func() any {
		return &reqData{reqLogCacher: newReqLogCacher()}
	},
}

func MiddlewareLogger() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {

			var rd *reqData
			{
				v := reqDataPool.Get().(*reqData)
				defer func() {
					v.reset()
					reqDataPool.Put(v)
				}()

				v.init()
				rd = v
			}

			ctx := context.WithValue(r.Context(), reqDataCtxKey{}, rd)
			rd.req = r.WithContext(ctx)

			defer func() {
				// TODO: detect if a log occurred and if so then log a correlation_log event

				// scheme := "http"
				// if req.TLS != nil {
				// 	scheme = "https"
				// }

				//

				// slog.String("http.flavor", req.Proto)
				// slog.String("url.scheme", scheme),
				// slog.String("http.request.header.host", req.Host),
				// slog.String("url.path", req.URL.Path),
				// if vv := req.Header["User-Agent"]; len(vv) > 0 {
				// 	if s := strings.TrimSpace(vv[0]); s != "" {
				// 		slog.String("user_agent.original", s)
				// 	}
				// }
			}()

			// TODO: can add context behaviors to rd type
			next.ServeHTTP(w, rd.req)
		})
	}
}

type xhttpLoggerInternalFactory struct {
	xslog.LoggerFactory
	attrsResolvers []attrsResolver
	maxAttrCount   int
	cacheLogger    bool
}

func (f *xhttpLoggerInternalFactory) Logger(ctx context.Context) xslog.Logger {
	logf := f.LoggerFactory

	if !RequestInFlight(ctx) {
		return logf.Logger(ctx)
	}

	rd := reqDataFromContext(ctx)
	if rd == nil {
		return logf.Logger(ctx)
	}

	if f.cacheLogger {
		if logger := rd.state.Load().(reqLogCacheState).logger; logger != nil {
			return logger.WithAttrs(ctx, slog.Duration(reqLoggerElapsedTimeKey, time.Since(rd.start)))
		}

		return rd.resolveWithCachedLogger(ctx, logf, f.maxAttrCount, f.attrsResolvers...)
	}

	if attrs := rd.state.Load().(reqLogCacheState).attrs; attrs != nil {
		attrs = append(attrs, slog.Duration(reqLoggerElapsedTimeKey, time.Since(rd.start)))
		return logf.Logger(ctx).WithAttrs(ctx, attrs...)
	}

	return rd.resolveWithCachedAttrs(ctx, logf, f.maxAttrCount, f.attrsResolvers...)

}

type xhttpLoggerFactory struct {
	xhttpLoggerInternalFactory
	w *xslog.LoggerWrappingFactory
}

func (f *xhttpLoggerFactory) Logger(ctx context.Context) xslog.Logger {
	return f.w
}
