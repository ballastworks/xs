package xpatterns

import (
	"context"
)

type ErrResponder[R any] interface {
	error
	Unwrap() error

	CausedBy(ctx context.Context, err error) R
}
