//go:build !linux

package filesystem

import (
	"context"
	"os"
)

func newCopyWriteback(*os.File) (func(context.Context, int64) error, error) {
	return func(ctx context.Context, _ int64) error { return ctx.Err() }, nil
}
