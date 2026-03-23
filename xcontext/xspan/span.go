package xspan

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
	"go.opentelemetry.io/otel/trace"
)

func recordError(ctx context.Context, err error, msg string) (string, trace.Span) {
	span := trace.SpanFromContext(ctx)
	if !span.IsRecording() {
		return "", nil
	}

	if err == nil {

		span.RecordError(err, trace.WithAttributes(
			semconv.ExceptionTypeKey.String("errorString"),
			semconv.ExceptionMessage(msg),
		))

		return msg, span
	}

	errMsg := err.Error()
	if msg != "" {
		errMsg = msg + ": " + errMsg
	}

	// TODO: do a bit of unwrapping here to get more meaningful exception attributes and types
	// versus just using the top level error type/message often used when rendering error responses.

	span.RecordError(err, trace.WithAttributes(
		semconv.ExceptionTypeKey.String(fmt.Sprintf("%T", err)),
		semconv.ExceptionMessage(errMsg),
	))

	return errMsg, span
}

func RecordError(ctx context.Context, err error, msg string) {
	if err == nil && msg == "" {
		return
	}

	recordError(ctx, err, msg)
}

func Fail(ctx context.Context, err error, msg string) {
	if err != nil || msg != "" {
		errMsg, span := recordError(ctx, err, msg)
		if span == nil {
			return
		}

		span.SetStatus(codes.Error, errMsg)
		return
	}

	span := trace.SpanFromContext(ctx)
	if span == nil {
		return
	}

	span.SetStatus(codes.Error, "")
}
