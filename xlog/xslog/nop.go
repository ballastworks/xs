package xslog

import (
	"context"
	"log/slog"

	"github.com/ballastworks/xs/xcontext/xspan"
)

var (
	_nopFactory = &nopFactory{}
	_nopHandler = &nopHandler{}
)

type nopFactory struct{}

func (nopFactory) Logger(ctx context.Context) Logger {
	return _nopFactory
}

func (nopFactory) Debug(ctx context.Context, msg string, attrs ...slog.Attr) {
}

func (nopFactory) Enabled(ctx context.Context, level slog.Level) bool {
	return false
}

func (nopFactory) Error(ctx context.Context, msg string, attrs ...slog.Attr) {
}

func (nopFactory) Info(ctx context.Context, msg string, attrs ...slog.Attr) {
}

func (nopFactory) Log(ctx context.Context, level slog.Level, msg string, attrs ...slog.Attr) {
}

func (nopFactory) LogUnchecked(ctx context.Context, level slog.Level, msg string, attrs ...slog.Attr) {
}

func (nopFactory) Warn(ctx context.Context, msg string, attrs ...slog.Attr) {
}

func (nopFactory) Handle(context.Context, slog.Record) error {
	return nil
}

func (nopFactory) WithGroup(string) slog.Handler {
	return _nopHandler
}

func (nopFactory) WithAttrs(ctx context.Context, attrs ...slog.Attr) Logger {
	return _nopFactory
}

func (nopFactory) WithErr(ctx context.Context, err error) Logger {
	return _nopFactory
}

func (nopFactory) SpanFail(ctx context.Context, err error, msg string, attrs ...slog.Attr) {
	xspan.Fail(ctx, err, msg)
}

func (nopFactory) SpanErr(ctx context.Context, err error, msg string, attrs ...slog.Attr) {
	xspan.RecordError(ctx, err, msg)
}

func (nopFactory) SlogHandler(context.Context) slog.Handler {
	return _nopHandler
}

type nopHandler struct {
}

func (nopHandler) Enabled(context.Context, slog.Level) bool {
	return false
}

func (nopHandler) Handle(context.Context, slog.Record) error {
	return nil
}

func (nopHandler) WithGroup(string) slog.Handler {
	return _nopHandler
}

func (nopHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return _nopHandler
}

func Nop() Logger {
	return _nopFactory
}

func NopFactory() LoggerFactory {
	return _nopFactory
}
