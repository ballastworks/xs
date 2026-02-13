package xhttp

import (
	"crypto/tls"
	"io"
	"net/http"
	"net/url"

	"github.com/ballastworks/xs/internal/xi_io"
)

// See http.Response for details.
//
// The only difference is that Body is a io.Reader and never needs to be closed.
//
// Also Write method is not available.
type ClientResponse struct {
	Status           string
	StatusCode       int
	Proto            string
	ProtoMajor       int
	ProtoMinor       int
	Header           http.Header
	Body             io.Reader
	ContentLength    int64
	TransferEncoding []string
	Close            bool
	Uncompressed     bool
	Trailer          http.Header
	Request          *http.Request
	TLS              *tls.ConnectionState
}

// Cookies is copied from (*http.Response).Cookies
func (r *ClientResponse) Cookies() []*http.Cookie {
	return (&http.Response{Header: r.Header}).Cookies()
}

// Location is copied from (*http.Response).Location
func (r *ClientResponse) Location() (*url.URL, error) {
	lv := r.Header.Get("Location")
	if lv == "" {
		return nil, http.ErrNoLocation
	}
	if r.Request != nil && r.Request.URL != nil {
		return r.Request.URL.Parse(lv)
	}
	return url.Parse(lv)
}

// ProtoAtLeast is copied from (*http.Response).ProtoAtLeast
func (r *ClientResponse) ProtoAtLeast(major, minor int) bool {
	return r.ProtoMajor > major ||
		r.ProtoMajor == major && r.ProtoMinor >= minor
}

// Release should be called after all references to the ClientResponse
// and the response body contained within it are no longer useful or
// referenced.
func (r *ClientResponse) Release() {
	if r == nil {
		return
	}

	// release the request body and any nop closer wrapping the body
	if body := r.Body; body != nil {
		r.Body = nil

		var r io.Reader
		switch v := body.(type) {
		case *xi_io.NopCloser:
			r = v.Reader
		case *xi_io.NopCloserWriterToReader:
			r = v.WriterToReader
		default:
			return
		}

		xi_io.ReleaseNopCloser(body)

		if r == nil {
			return
		}

		if v, ok := r.(interface{ Release() }); ok && v != nil {
			v.Release()
		}
	}

	// zero out the value so it's clear nothing about it is valid anymore
	*r = ClientResponse{}
}

func newClientResponse(r *http.Response) *ClientResponse {
	if r == nil {
		return nil
	}

	return &ClientResponse{
		Status:           r.Status,
		StatusCode:       r.StatusCode,
		Proto:            r.Proto,
		ProtoMajor:       r.ProtoMajor,
		ProtoMinor:       r.ProtoMinor,
		Header:           r.Header,
		Body:             r.Body,
		ContentLength:    r.ContentLength,
		TransferEncoding: r.TransferEncoding,
		Close:            r.Close,
		Uncompressed:     r.Uncompressed,
		Trailer:          r.Trailer,
		Request:          r.Request,
		TLS:              r.TLS,
	}
}

func StatusCode[T ClientResponse | http.Response](pr interface{ ProtocolResponse() *T }) int {

	if pr == nil {
		return 0
	}

	if resp := pr.ProtocolResponse(); resp != nil {
		switch v := any(resp).(type) {
		case *ClientResponse:
			if v != nil {
				return v.StatusCode
			}
		case *http.Response:
			if v != nil {
				return v.StatusCode
			}
		}
	}

	return 0
}
