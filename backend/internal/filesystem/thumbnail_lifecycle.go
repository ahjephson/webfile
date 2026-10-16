package filesystem

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"webfile/internal/thumbnailcache"
)

const managedThumbnailDimension = 320

func (s *Service) thumbnailLocation(virtual string) (thumbnailcache.Location, bool) {
	name, relative, err := SplitVirtual(virtual)
	if err != nil || relative == "" {
		return thumbnailcache.Location{}, false
	}
	root, ok := s.resolver.roots[name]
	if !ok {
		return thumbnailcache.Location{}, false
	}
	names := strings.Split(relative, "/")
	for index, name := range names {
		names[index] = thumbnailcache.HashName(name)
	}
	return thumbnailcache.Location{Root: root.cacheID, Names: names}, true
}

func (s *Service) thumbnailTicket(ctx context.Context, virtual, key, variant string) *thumbnailcache.LocationTicket {
	cache, ok := s.thumbnailCache.(thumbnailcache.LocationCache)
	if !ok {
		return nil
	}
	location, ok := s.thumbnailLocation(virtual)
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	result, err := cache.Locations(ctx, thumbnailcache.LocationRequest{Action: "begin", Paths: []thumbnailcache.Location{location}, Key: key, Variant: variant})
	if err != nil || len(result.Tickets) != 1 {
		return nil
	}
	return &result.Tickets[0]
}

func (s *Service) linkThumbnail(ctx context.Context, source thumbnailSource) {
	if source.ticket == nil || source.ticket.Linked {
		return
	}
	cache, ok := s.thumbnailCache.(thumbnailcache.LocationCache)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	_, _ = cache.Locations(ctx, thumbnailcache.LocationRequest{Action: "link", Ticket: *source.ticket, Key: source.key, Variant: thumbnailVariant(source.contentType, source.width, source.height)})
}

func thumbnailVariant(contentType string, width, height int) string {
	return thumbnailcache.HashName(contentType + ":" + thumbnailSize(width, height))
}

// Capture all selected roots in one cache request, never once per child file.
// This also avoids adding IPC to the small-file copy/delete pipeline.
func (s *Service) captureThumbnailChanges(ctx context.Context, sources []string) []thumbnailcache.LocationTicket {
	cache, ok := s.thumbnailCache.(thumbnailcache.LocationCache)
	if !ok {
		return nil
	}
	var locations []thumbnailcache.Location
	for _, source := range sources {
		location, ok := s.thumbnailLocation(source)
		if !ok {
			return nil
		}
		locations = append(locations, location)
	}
	if len(locations) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	result, err := cache.Locations(ctx, thumbnailcache.LocationRequest{Action: "capture", Paths: locations})
	if err != nil {
		return nil
	}
	return result.Tickets
}

func (s *Service) applyThumbnailChanges(changes []thumbnailcache.LocationChange) {
	cache, ok := s.thumbnailCache.(thumbnailcache.LocationCache)
	if !ok || len(changes) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, _ = cache.Locations(ctx, thumbnailcache.LocationRequest{Action: "apply", Changes: changes})
}

type thumbnailTransferCapture struct {
	ticket       thumbnailcache.LocationTicket
	targetTicket thumbnailcache.LocationTicket
	kind         string
	source       string
	destination  string
	info         os.FileInfo
	targetInfo   os.FileInfo
}

func (s *Service) captureThumbnailTransfer(ctx context.Context, kind string, sources []string, destination string) []thumbnailTransferCapture {
	if _, ok := s.thumbnailCache.(thumbnailcache.LocationCache); !ok {
		return nil
	}
	var paths []string
	var captured []thumbnailTransferCapture
	for _, source := range sources {
		_, absolute, err := s.resolver.ResolveEntry(source)
		if err != nil {
			return nil
		}
		info, err := os.Lstat(absolute)
		if err != nil {
			continue
		}
		target := VirtualJoin(destination, filepath.Base(absolute))
		var targetInfo os.FileInfo
		if _, targetAbsolute, err := s.resolver.ResolveForCreate(target); err == nil {
			targetInfo, _ = os.Lstat(targetAbsolute)
		}
		paths = append(paths, source, target)
		captured = append(captured, thumbnailTransferCapture{kind: kind, source: source, destination: target, info: info, targetInfo: targetInfo})
	}
	tickets := s.captureThumbnailChanges(ctx, paths)
	if len(tickets) != len(paths) {
		return nil
	}
	for index := range captured {
		captured[index].ticket = tickets[2*index]
		captured[index].targetTicket = tickets[2*index+1]
	}
	return captured
}

func (s *Service) finishThumbnailTransfer(captured []thumbnailTransferCapture) {
	var changes []thumbnailcache.LocationChange
	for _, item := range captured {
		_, target, targetErr := s.resolver.ResolveEntry(item.destination)
		if targetErr == nil {
			if info, err := os.Lstat(target); err == nil && (item.targetInfo == nil || !os.SameFile(item.targetInfo, info)) {
				changes = append(changes, thumbnailcache.LocationChange{Ticket: item.targetTicket})
			}
		}
		if item.kind != "move" {
			continue
		}
		_, absolute, err := s.resolver.ResolveForCreate(item.source)
		if err != nil {
			continue
		}
		if _, err := os.Lstat(absolute); !os.IsNotExist(err) {
			continue
		}
		change := thumbnailcache.LocationChange{Ticket: item.ticket}
		if item.info.IsDir() {
			_, target, err := s.resolver.ResolveEntry(item.destination)
			if err == nil {
				info, err := os.Lstat(target)
				if err == nil && os.SameFile(item.info, info) {
					if location, ok := s.thumbnailLocation(item.destination); ok {
						change.Destination = &location
					}
				}
			}
		}
		// Merge/copy-delete and individual renames retire old references, keeping
		// blobs under ordinary eviction for possible authorized on-demand reuse.
		changes = append(changes, change)
	}
	s.applyThumbnailChanges(changes)
}
