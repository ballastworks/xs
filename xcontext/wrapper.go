package xcontext

import (
	"context"
)

type Wrapper interface {
	WrapContext(context.Context) context.Context
	CleanupWrappedContext()
}
