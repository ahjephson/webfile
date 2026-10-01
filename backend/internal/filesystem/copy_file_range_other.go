//go:build !linux

package filesystem

import (
	"context"
	"os"
)

func copyFileRangePlatform(context.Context, *os.File, *os.File, func(int64)) (bool, error) {
	return false, nil
}
