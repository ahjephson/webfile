package filesystem

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
)

const managedThumbnailDimension = 320

type cachedThumbnail struct {
	relative string
	key      string
}

func (s *Service) collectCachedThumbnails(ctx context.Context, virtualPath string) []cachedThumbnail {
	_, absolute, err := s.resolver.ResolveEntry(virtualPath)
	if err != nil {
		return nil
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return nil
	}
	if !info.IsDir() {
		return s.collectCachedThumbnail(ctx, virtualPath, ".")
	}

	items := make([]cachedThumbnail, 0)
	_ = filepath.WalkDir(absolute, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || ctx.Err() != nil {
			return fs.SkipDir
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return nil
		}
		relative, err := filepath.Rel(absolute, current)
		if err != nil {
			return nil
		}
		items = append(items, s.collectCachedThumbnail(ctx, VirtualJoin(virtualPath, filepath.ToSlash(relative)), filepath.ToSlash(relative))...)
		return nil
	})
	return items
}

func (s *Service) collectCachedThumbnail(ctx context.Context, virtualPath, relative string) []cachedThumbnail {
	source, file, err := s.inspectThumbnailSource(ctx, virtualPath, managedThumbnailDimension, managedThumbnailDimension)
	if err != nil {
		return nil
	}
	_ = file.Close()
	if _, ok, err := s.thumbnailCache.Get(ctx, source.key); err != nil || !ok {
		return nil
	}
	return []cachedThumbnail{{relative: relative, key: source.key}}
}

func (s *Service) moveCachedThumbnails(ctx context.Context, cached []cachedThumbnail, destination string) {
	for _, item := range cached {
		virtualPath := destination
		if item.relative != "." {
			virtualPath = VirtualJoin(destination, item.relative)
		}
		source, file, err := s.inspectThumbnailSource(ctx, virtualPath, managedThumbnailDimension, managedThumbnailDimension)
		if err != nil {
			continue
		}
		_ = file.Close()
		_ = s.thumbnailCache.Move(ctx, item.key, source.key)
	}
}

func (s *Service) deleteCachedThumbnails(ctx context.Context, cached []cachedThumbnail) {
	for _, item := range cached {
		_ = s.thumbnailCache.Delete(ctx, item.key)
	}
}
