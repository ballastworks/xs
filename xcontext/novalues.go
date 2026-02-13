package xcontext

import (
	"context"
	"time"
)

type noValuesCtx struct {
	ctx context.Context
}

func (c noValuesCtx) Deadline() (time.Time, bool) {
	return c.ctx.Deadline()
}

func (c noValuesCtx) Done() <-chan struct{} {
	return c.ctx.Done()
}

func (c noValuesCtx) Err() error {
	return c.ctx.Err()
}

func (c noValuesCtx) Value(key any) any {
	return nil
}

func WithoutValues(ctx context.Context) context.Context {
	if ctx == nil {
		panic("cannot create context from nil parent")
	}
	return noValuesCtx{ctx}
}
