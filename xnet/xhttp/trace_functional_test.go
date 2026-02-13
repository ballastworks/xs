package xhttp_test

// var _ = Context("TraceAdder middleware", func() {
// 	Context("given an empty context", func() {
// 		var ctx context.Context
// 		{
// 			var cancel func()
// 			BeforeEach(func() {
// 				ctx, cancel = context.WithCancel(context.Background())
// 			})
// 			AfterEach(func() {
// 				cancel()
// 			})
// 		}

// 		DescribeTable("calling AddTrace() in handler results in correct header values being set",
// 			func(headerKeys []string, options ...xhttp.TraceMiddlewareOption) {
// 				h := http.Header{}
// 				i := 0
// 				for _, k := range headerKeys {
// 					h[k] = []string{strconv.Itoa(i)}
// 					i++
// 				}

// 				req := httptest.NewRequest(http.MethodGet, "https://www.example.com", http.NoBody).WithContext(ctx)
// 				req.Header = h
// 				w := httptest.NewRecorder()

// 				dst := &http.Request{Header: http.Header{}}
// 				xhttp.AddTrace(ctx, dst)
// 				Expect(dst).To(Equal(&http.Request{Header: http.Header{}}))

// 				var callCount int
// 				middleware, err := xhttp.NewTraceMiddleware(options...)
// 				Expect(err).To(BeNil())
// 				handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 					callCount++
// 					Expect(callCount).To(Equal(1))
// 					dst := &http.Request{Header: http.Header{}}
// 					xhttp.AddTrace(r.Context(), dst)
// 					Expect(dst).To(Equal(&http.Request{Header: h}))
// 					// running again to cover short-circuit paths because it's already been computed
// 					dst = &http.Request{Header: http.Header{}}
// 					xhttp.AddTrace(r.Context(), dst)
// 					Expect(dst).To(Equal(&http.Request{Header: h}))
// 				}))
// 				handler.ServeHTTP(w, req)
// 				Expect(callCount).To(Equal(1))
// 			},
// 			Entry("light step header keys", []string{"X-Ot-Span-Context"}),
// 			Entry("zipkin header keys", []string{"B3", "X-B3-Traceid", "X-B3-Spanid", "X-B3-Parentspanid", "X-B3-Sampled", "X-B3-Flags"}),
// 			Entry("datadog", []string{"X-Datadog-Trace-Id", "X-Datadog-Parent-Id", "X-Datadog-Sampling-Priority"}),
// 			Entry("sky walking", []string{"Sw8"}),
// 			Entry("amazon xray", []string{"X-Amzn-Trace-Id"}),
// 			Entry("custom", []string{"Rawr"}, xhttp.TraceMiddlewareOpts().KeysFunc(func() []string { return []string{"Rawr"} })),
// 		)

// 		It("nil header - which should not be possible in normal operations", func() {
// 			req := httptest.NewRequest(http.MethodGet, "https://www.example.com", http.NoBody).WithContext(ctx)
// 			req.Header = nil
// 			w := httptest.NewRecorder()

// 			var callCount int
// 			middleware, err := xhttp.NewTraceMiddleware()
// 			Expect(err).To(BeNil())
// 			handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 				callCount++
// 				Expect(callCount).To(Equal(1))
// 				nreq := &http.Request{Header: http.Header{}}
// 				xhttp.AddTrace(r.Context(), nreq)
// 				Expect(nreq).To(Equal(&http.Request{Header: http.Header{}}))
// 			}))
// 			handler.ServeHTTP(w, req)
// 			Expect(callCount).To(Equal(1))
// 		})

// 		It("nil request or nil req Header - which should not be possible in normal operations", func() {
// 			req := httptest.NewRequest(http.MethodGet, "https://www.example.com", http.NoBody).WithContext(ctx)
// 			req.Header = nil
// 			w := httptest.NewRecorder()

// 			var callCount int
// 			middleware, err := xhttp.NewTraceMiddleware()
// 			Expect(err).To(BeNil())
// 			handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
// 				callCount++
// 				Expect(func() {
// 					var req *http.Request
// 					xhttp.AddTrace(r.Context(), req)
// 				}).ToNot(Panic())
// 				Expect(func() {
// 					req := &http.Request{}
// 					xhttp.AddTrace(r.Context(), req)
// 				}).ToNot(Panic())
// 			}))
// 			handler.ServeHTTP(w, req)
// 			Expect(callCount).To(Equal(1))
// 		})

// 		It("nil KeysFunc - should result in error on NewTraceMiddleware call", func() {
// 			middleware, err := xhttp.NewTraceMiddleware(xhttp.TraceMiddlewareOpts().KeysFunc(nil))
// 			Expect(middleware).To(BeNil())
// 			Expect(err).ToNot(BeNil())
// 			Expect(err).To(Equal(xhttp.ErrNilKeysFunc))
// 		})
// 	})
// })

// var _ = Context("AddTrace without middleware", func() {
// 	Context("given an empty context", func() {
// 		var ctx context.Context
// 		{
// 			var cancel func()
// 			BeforeEach(func() {
// 				ctx, cancel = context.WithCancel(context.Background())
// 			})
// 			AfterEach(func() {
// 				cancel()
// 			})
// 		}

// 		Context("given an empty header", func() {
// 			dst := &http.Request{Header: http.Header{}}

// 			When("calling AddTrace", func() {

// 				It("should still have a header with no keys", func() {
// 					xhttp.AddTrace(ctx, dst)
// 					Expect(len(dst.Header)).To(Equal(0))
// 				})
// 			})
// 		})

// 		Context("given a nil header", func() {
// 			dst := &http.Request{}

// 			When("calling AddTrace", func() {

// 				It("should keep a nil header value", func() {
// 					xhttp.AddTrace(ctx, dst)
// 					Expect(dst.Header).To(BeNil())
// 				})
// 			})
// 		})

// 		Context("given a nil request", func() {
// 			var dst *http.Request

// 			When("calling AddTrace", func() {

// 				It("does not panic", func() {
// 					Expect(func() {
// 						xhttp.AddTrace(ctx, dst)
// 					}).ToNot(Panic())
// 				})
// 			})
// 		})
// 	})
// })
