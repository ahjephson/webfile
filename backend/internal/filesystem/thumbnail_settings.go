package filesystem

import (
	"context"
	"errors"
	"sync"
	"time"
	"webfile/internal/thumbnailsettings"
)

var ErrThumbnailsDisabled = errors.New("thumbnail generation is disabled")

func (s *Service) ThumbnailSettings() thumbnailsettings.Settings {
	s.thumbnailSettingsMu.Lock()
	defer s.thumbnailSettingsMu.Unlock()
	return s.thumbnailSettings
}

func (s *Service) ConfigureThumbnails(settings thumbnailsettings.Settings) error {
	if err := settings.Validate(); err != nil {
		return err
	}
	s.thumbnailSettingsMu.Lock()
	defer s.thumbnailSettingsMu.Unlock()
	if s.thumbnailSettings == settings {
		return nil
	}
	s.thumbnailSettings = settings
	images, videos := settings.ImageWorkers, settings.VideoWorkers
	if !settings.ImagesEnabled {
		images = 0
	}
	if !settings.VideosEnabled {
		videos = 0
	}
	s.thumbnailSlots.setLimit(images)
	s.videoQueue.configure(videos, settings.MaxQueued, time.Duration(settings.VideoTimeoutSeconds)*time.Second)
	return nil
}

// A resized limiter preserves active work while changing the limit for new work.
type thumbnailLimiter struct {
	mu            sync.Mutex
	limit, active int
	changed       chan struct{}
}

func newThumbnailLimiter(limit int) *thumbnailLimiter {
	return &thumbnailLimiter{limit: limit, changed: make(chan struct{})}
}
func (l *thumbnailLimiter) notify() { close(l.changed); l.changed = make(chan struct{}) }
func (l *thumbnailLimiter) setLimit(limit int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.limit = limit
	l.notify()
}
func (l *thumbnailLimiter) acquire(ctx context.Context) error {
	for {
		l.mu.Lock()
		if l.limit == 0 {
			l.mu.Unlock()
			return ErrThumbnailsDisabled
		}
		if l.active < l.limit {
			l.active++
			l.mu.Unlock()
			return nil
		}
		changed := l.changed
		l.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
func (l *thumbnailLimiter) release() { l.mu.Lock(); defer l.mu.Unlock(); l.active--; l.notify() }

func (q *videoThumbnailQueue) configure(workers, maxQueued int, timeout time.Duration) {
	q.mu.Lock()
	select {
	case <-q.stop:
		q.mu.Unlock()
		return
	default:
	}
	q.workers, q.maxQueued, q.timeout = workers, maxQueued, timeout
	q.disabled = workers == 0
	for q.spawned < workers {
		q.spawned++
		q.workerWait.Add(1)
		go q.runWorker()
	}
	var ids []string
	if workers == 0 {
		for id, job := range q.ids {
			if job.view.Cancellable {
				job.retryWhenEnabled = true
				ids = append(ids, id)
			}
		}
	}
	q.mu.Unlock()
	for _, id := range ids {
		q.cancelJob(id)
	}
	for i := 0; i < workers; i++ {
		q.signal()
	}
}
