package filesystem

import "context"

const defaultCopyFileRangeChunk = 4 * 1024 * 1024

type copyChunkSizeContextKey struct{}

// WithCopyChunkSize applies a validated per-operation copy_file_range limit.
// Other copy paths retain the default when the context carries no override.
func WithCopyChunkSize(ctx context.Context, bytes int) context.Context {
	if bytes <= 0 {
		return ctx
	}
	return context.WithValue(ctx, copyChunkSizeContextKey{}, bytes)
}

func copyChunkSize(ctx context.Context) int {
	if size, ok := ctx.Value(copyChunkSizeContextKey{}).(int); ok && size > 0 {
		return size
	}
	return defaultCopyFileRangeChunk
}
