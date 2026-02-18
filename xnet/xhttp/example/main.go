package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/ballastworks/xs/xcontext"
	"github.com/ballastworks/xs/xerrors"
	"github.com/ballastworks/xs/xlog/xslog"
	"github.com/ballastworks/xs/xnet/xhttp"
	"github.com/ballastworks/xs/xnet/xhttp/xplog"
	"go.opentelemetry.io/contrib/instrumentation/host"
	"go.opentelemetry.io/contrib/instrumentation/runtime"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/stdout/stdoutmetric"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
)

var (
	errPanicking = errors.New("panicking")
)

func otelDisabled() bool {
	if s := os.Getenv("OTEL_SDK_DISABLED"); s != "" {
		if b, err := strconv.ParseBool(s); err == nil {
			return b
		}
	}

	return false
}

func otelMetricsDisabled() bool {
	if s := os.Getenv("OTEL_METRICS_DISABLED"); s != "" {
		if b, err := strconv.ParseBool(s); err == nil {
			return b
		}
	}

	return false
}

// setupOTEL instruments otel metrics and tracing.
//
// Returns a function that should be deferred to cleanup the asynchronous
// workers spawned for the instrumentation.
func setupOTEL(ctx context.Context, logger xslog.Logger) (teardownOTEL func()) {
	if otelDisabled() {
		logger.Warn(ctx, "otel setup disabled")
		return nil
	}

	logger.Info(ctx, "otel setup enabled")

	deferFuncs := make([]func(), 0, 7)
	defer func() {
		if len(deferFuncs) == 0 {
			teardownOTEL = nil
			return
		}

		teardownOTEL = func() {
			for i := range len(deferFuncs) - 1 {
				f := deferFuncs[i]
				defer f()
			}

			f := deferFuncs[len(deferFuncs)-1]
			f()
		}
	}()

	const numPossibleErrSignals = 5 * 2 // (2 is for the panic catchers as well)

	var shutdownWG, metricsWG sync.WaitGroup
	errChan := make(chan error, numPossibleErrSignals)
	errToJoin := make([]error, 0, numPossibleErrSignals)

	// wait for concurrent goroutines shutdown to finish
	deferFuncs = append(deferFuncs, func() {
		shutdownWG.Wait()
		metricsWG.Wait()
		close(errChan)

		for err := range errChan {
			errToJoin = append(errToJoin, err)
		}

		if len(errToJoin) == 0 {
			logger.Info(ctx, "otel instruments offline: graceful shutdown successful")
			return
		}

		err := errors.Join(errToJoin...)
		logger.WithErr(ctx, err).Error(ctx, "otel graceful shutdown failed")

		panic(err)
	})

	// loads env variables such as OTEL_SERVICE_NAME, OTEL_SERVICE_VERSION, etc.
	// to create a resource describing the service
	//
	// see https://opentelemetry.io/docs/specs/otel/configuration/sdk-environment-variables/
	//
	// to provide version details as well as vcs info, set env variables:
	//		OTEL_SERVICE_NAME=my-service
	//		OTEL_SERVICE_VERSION=1.2.3
	//		OTEL_RESOURCE_ATTRIBUTES (CDL):
	// 			- deployment.environment=production
	// 			- service.instance.id=instance-1
	// 			- service.version.commit=abc123
	// 			- service.version.build_date=2024-01-01T00:00:00Z
	//
	// see https://opentelemetry.io/docs/specs/otel/semantic-conventions/

	// defined service resource meta used in metric exporters
	svcRes, err := resource.New(ctx,
		resource.WithFromEnv(),
		resource.WithProcess(),
		resource.WithHost(),
	)
	if err != nil {
		panic(fmt.Errorf("failed to create service resource: %w", err))
	}

	if !otelMetricsDisabled() {

		var metricExp metric.Exporter
		// initialize the metric exporter
		{
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "")
			enc.SetEscapeHTML(false)

			e, err := stdoutmetric.New(stdoutmetric.WithEncoder(enc))
			if err != nil {
				panic(fmt.Errorf("failed to create stdout default metric exporter: %w", err))
			}

			deferFuncs = append(deferFuncs, func() {
				shutdownWG.Add(1)
				go func() {
					defer shutdownWG.Done()
					defer func() {
						if r := recover(); r != nil {
							defer panic(r)

							errChan <- errPanicking
						}
					}()

					// wait for usage of the metrics exporter to stop
					metricsWG.Wait()

					if err := e.Shutdown(context.Background()); err != nil {
						logger.WithErr(ctx, err).Error(ctx,
							"failed to shutdown metric exporter",
						)

						errChan <- err
					}
				}()
			})

			metricExp = nopShutdownMetricExporter{e}
		}

		// initialize metrics provider
		{

			mp := metric.NewMeterProvider(
				metric.WithResource(svcRes),
				metric.WithReader(metric.NewPeriodicReader(
					metricExp,
					// default interval is 60s, default (flush) timeout is 30s
					metric.WithInterval(10*time.Second),
					metric.WithTimeout(30*time.Second),
				)),
			)
			deferFuncs = append(deferFuncs, func() {
				metricsWG.Add(1)
				go func() {
					defer metricsWG.Done()
					defer func() {
						if r := recover(); r != nil {
							defer panic(r)

							errChan <- errPanicking
						}
					}()

					err := mp.Shutdown(context.Background())
					if err != nil {
						logger.WithErr(ctx, err).Error(ctx,
							"failed to shutdown meter provider",
						)

						errChan <- err
					}
				}()
			})

			otel.SetMeterProvider(mp)
		}

		// report host metrics every second
		{
			// TODO: host metrics such as inodes, open file handles, open ports, disk, and network util should be handled by the app-mesh
			// this likely can all go away

			// https://github.com/open-telemetry/opentelemetry-go-contrib/blob/main/instrumentation/host/example/main.go

			// Register the exporter with an SDK via a periodic reader.
			mp := metric.NewMeterProvider(
				metric.WithResource(svcRes),
				metric.WithReader(metric.NewPeriodicReader(
					metricExp,
					metric.WithInterval(1*time.Second),
				)),
			)
			deferFuncs = append(deferFuncs, func() {
				metricsWG.Add(1)
				go func() {
					defer metricsWG.Done()
					defer func() {
						if r := recover(); r != nil {
							defer panic(r)

							errChan <- errPanicking
						}
					}()

					err := mp.Shutdown(context.Background())
					if err != nil {
						logger.WithErr(ctx, err).Error(ctx,
							"failed to gracefully shutdown service host metrics",
						)

						errChan <- err
					}
				}()
			})

			err := host.Start(host.WithMeterProvider(mp))
			if err != nil {
				panic(fmt.Errorf("failed to start host metrics exporter: %w", err))
			}
		}

		// report runtime metrics every second
		{
			// https://github.com/open-telemetry/opentelemetry-go-contrib/blob/main/instrumentation/runtime/example/main.go

			// Register the exporter with an SDK via a periodic reader.
			mp := metric.NewMeterProvider(
				metric.WithResource(svcRes),
				metric.WithReader(metric.NewPeriodicReader(
					metricExp,
					metric.WithInterval(1*time.Second),
				)),
			)
			deferFuncs = append(deferFuncs, func() {
				metricsWG.Add(1)
				go func() {
					defer metricsWG.Done()
					defer func() {
						if r := recover(); r != nil {
							defer panic(r)

							errChan <- errPanicking
						}
					}()

					err := mp.Shutdown(context.Background())
					if err != nil {
						logger.WithErr(ctx, err).Error(ctx,
							"failed to gracefully shutdown service runtime metrics",
						)

						errChan <- err
					}
				}()
			})

			err := runtime.Start(runtime.WithMinimumReadMemStatsInterval(1 * time.Second))
			if err != nil {
				panic(fmt.Errorf("failed to start runtime metrics exporter: %w", err))
			}
		}

	} else {
		logger.Warn(ctx, "otel metrics disabled")
	}

	// initialize tracer provider
	{
		// TODO: first http.request layer of traces should be provided by the app-mesh, not the container

		// https://github.com/open-telemetry/opentelemetry-go-contrib/blob/main/instrumentation/net/http/otelhttp/example/client/client.go

		// Create stdout exporter to be able to retrieve
		// the collected spans.
		exporter, err := stdouttrace.New()
		if err != nil {
			panic(fmt.Errorf("failed to create tracer provider: %w", err))
		}

		// For the demonstration, use trace.AlwaysSample sampler to sample all traces.
		// In a production application, use trace.ProbabilitySampler with a desired probability.
		tp := trace.NewTracerProvider(
			trace.WithResource(svcRes),
			trace.WithSampler(trace.AlwaysSample()),
			trace.WithBatcher(exporter),
		)
		deferFuncs = append(deferFuncs, func() {
			shutdownWG.Add(1)
			go func() {
				defer shutdownWG.Done()
				defer func() {
					if r := recover(); r != nil {
						defer panic(r)

						errChan <- errPanicking
					}
				}()

				err := tp.Shutdown(context.Background())
				if err != nil {
					logger.WithErr(ctx, err).Error(ctx,
						"failed to gracefully shutdown tracer provider",
					)

					errChan <- err
				}
			}()
		})

		otel.SetTracerProvider(tp)
		otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

		deferFuncs = append(deferFuncs, func() {
			logger.Info(ctx, "otel instruments gracefully shutting down")
		})
	}

	return
}

type nopShutdownMetricExporter struct {
	metric.Exporter
}

func (nopShutdownMetricExporter) Shutdown(context.Context) error { return nil }

func main() {
	xcontext.WithRootShutdownSignalContext(func(ctx context.Context, cancel context.CancelCauseFunc, setRCLogger func(xslog.Logger)) {
		logger, err := xslog.NewEnvLogger()
		if err != nil {
			panic(err)
		}
		setRCLogger(logger)
		logf := xslog.StaticFactory(logger)
		xslog.SetDefaultFactory(logf)

		//
		// setup tracer
		//

		if teardownOTEL := setupOTEL(ctx, logger); teardownOTEL != nil {
			defer teardownOTEL()
		}

		//
		// create a new main tracer
		// and perform more setup within a "setup" span
		//

		tracer := otel.Tracer("main")

		var srv *xhttp.Server
		{
			ctx, span := tracer.Start(ctx, "setup")
			defer span.End()
			_ = ctx // TODO: remove this line if ctx is used in setup steps below in the future, otherwise it remains as a defensive measure for tracing context properly.

			var reqLogf xslog.LoggerFactory
			{
				op := xhttp.RequestLoggerFactoryOpts()

				v, err := xhttp.NewRequestLoggerFactory(
					op.LoggerFactory(logf),
					op.CacheLogger(true),
					// op.RecordPotentialGatewayPII(true),
				)
				if err != nil {
					panic(err)
				}

				reqLogf = v
			}
			xhttp.SetDefaultLoggerFactory(reqLogf)

			// The default logger factory implementation repeatedly resolves the default global logger via atomic reads. Setting the implementation with a specific logger instance here removes those atomic reads.
			respf, err := xhttp.NewResponseFactory(
				xhttp.ResponseFactoryOpts().LoggerFactory(reqLogf),
			)
			if err != nil {
				logger.WithErr(ctx, err).Error(ctx, "failed to create a new default response factory")
				panic(err)
			}
			xhttp.SetDefaultResponseFactory(respf)

			//
			// Load default options
			//

			listenHost := "127.0.0.1"
			listenPort := "8080"

			if s, ok := os.LookupEnv("LISTEN_HOST"); ok {
				listenHost = s
			}

			if s, ok := os.LookupEnv("LISTEN_PORT"); ok {
				var v string
				n, err := strconv.ParseUint(s, 10, 16)
				if err == nil {
					v = strconv.Itoa(int(n))
				}
				if err != nil || v != s {
					logger.WithErr(ctx, err).Error(ctx, "failed to parse listen port")
					panic(err)
				}

				listenPort = v
			}

			//
			// Setup Router
			//

			op := xhttp.RouterOpts()
			rt, err := xhttp.NewRouter(
				op.LoggerFactory(reqLogf),
				op.IgnoreTrailingSlash(true),
			)
			if err != nil {
				panic(err)
			}

			alwaysOKHandler := xhttp.NewFluentResp().
				JsonBody(json.RawMessage(`{"status":"ok"}`)).
				StaticHandler()

			rt.Get("/", alwaysOKHandler)

			{
				ErrErrorEndpointError := errors.New("error endpoint always fails")

				rt.GetE("/error", func(w http.ResponseWriter, r *http.Request) error {
					// logger := xplog.ProtoLogger(r)
					// logger.Error("foo")
					// logger.Error("bar")

					ctx := r.Context()

					logger := xplog.Logger(ctx)
					logger.Error(ctx, "foo")
					logger.Error(ctx, "bar")

					return xerrors.WithStack(ErrErrorEndpointError)
				})
			}

			// service instance ready to start handling traffic probe handler
			//
			// may sample upstream services to make sure they are healthy before
			// returning a likely sticky success response
			rt.Get("/readyz", alwaysOKHandler)

			// ongoing liveness probe handler
			//
			// if this ever returns an error it's a strong signal
			// for the orchestration layers to terminate the instance
			rt.Get("/healthz", alwaysOKHandler)

			//
			// Setup Server
			//

			srv, err = xhttp.NewServer(
				xhttp.SrvOpts().Server(&http.Server{
					Addr:    net.JoinHostPort(listenHost, listenPort),
					Handler: rt,
				}),
				xhttp.SrvOpts().LoggerFactory(reqLogf),
			)
			if err != nil {
				panic(err)
			}

			span.End()
		}

		//
		// Run Server
		//

		// note: isolating the serveCTX and span to ensure that in traces, the ListenAndServe operation is clearly identifiable and not considered relevant for any child request contexts handled by the server instance.
		serveCTX, span := tracer.Start(ctx, "listen_and_serve")
		defer span.End()

		if err := srv.ListenAndServe(ctx); err != nil {
			logger.SpanFail(serveCTX, err, "listen and serve error")
			panic(err)
		}
	})
}
