package xhttp_test

// import (
// 	// "net/http"

// 	// "github.com/ballastworks/xs/xnet"
// 	// "github.com/ballastworks/xs/xnet/xhttp"
// 	. "github.com/onsi/ginkgo/v2"
// 	// . "github.com/onsi/gomega"
// )

// var _ = Context("xhttp.StatusCode tests", func() {
// 	// type HttpRespTarget struct {
// 	// 	xnet.Response[http.Response]
// 	// }
// 	// type XHttpRespTarget struct {
// 	// 	xnet.Response[xhttp.Response]
// 	// }

// 	// Context("given nil response objects", func() {
// 	// 	var httpResp HttpRespTarget
// 	// 	var xHttpResp XHttpRespTarget

// 	// 	It("returns zero when passed to StatusCode", func() {
// 	// 		var sc int
// 	// 		sc = xhttp.StatusCode(httpResp)
// 	// 		Expect(sc).To(Equal(0))
// 	// 		// should keep returning zero when explicitly set to nil
// 	// 		httpResp.SetProtocolResponse(nil)
// 	// 		sc = xhttp.StatusCode(httpResp)
// 	// 		Expect(sc).To(Equal(0))

// 	// 		sc = xhttp.StatusCode(xHttpResp)
// 	// 		Expect(sc).To(Equal(0))
// 	// 		// should keep returning zero when explicitly set to nil
// 	// 		xHttpResp.SetProtocolResponse(nil)
// 	// 		sc = xhttp.StatusCode(xHttpResp)
// 	// 		Expect(sc).To(Equal(0))
// 	// 	})
// 	// })

// 	// Context("given 200 response objects", func() {
// 	// 	var httpResp HttpRespTarget
// 	// 	var xHttpResp XHttpRespTarget
// 	// 	{
// 	// 		BeforeEach(func() {
// 	// 			httpResp.SetProtocolResponse(&http.Response{StatusCode: 200})
// 	// 			xHttpResp.SetProtocolResponse(&xhttp.Response{StatusCode: 200})
// 	// 		})
// 	// 	}

// 	// 	It("returns 200 when passed to StatusCode", func() {
// 	// 		var sc int
// 	// 		sc = xhttp.StatusCode(httpResp)
// 	// 		Expect(sc).To(Equal(200))
// 	// 		sc = xhttp.StatusCode(xHttpResp)
// 	// 		Expect(sc).To(Equal(200))
// 	// 	})
// 	// })
// })
