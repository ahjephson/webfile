package filesystem

import (
	"context"
	"errors"
	"testing"
	"time"
	"webfile/internal/thumbnailsettings"
)

func TestThumbnailLimiterResizeAndDisable(t *testing.T) {
	limiter := newThumbnailLimiter(1)
	if err := limiter.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := limiter.acquire(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("limit not enforced: %v", err)
	}
	limiter.setLimit(2)
	if err := limiter.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	limiter.setLimit(0)
	if err := limiter.acquire(context.Background()); !errors.Is(err, ErrThumbnailsDisabled) {
		t.Fatalf("disabled: %v", err)
	}
	limiter.release()
	limiter.release()
	limiter.setLimit(1)
	if err := limiter.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	limiter.release()
}

func TestThumbnailSettingsCancelAndReenableQueue(t *testing.T) {
	service := NewService(nil, 1024)
	defer service.Close()
	settings := thumbnailsettings.Default()
	settings.VideoWorkers = 4
	if err := service.ConfigureThumbnails(settings); err != nil {
		t.Fatal(err)
	}
	queue := service.videoQueue
	queue.mu.Lock()
	workers, spawned := queue.workers, queue.spawned
	queue.mu.Unlock()
	if workers != 4 || spawned != 4 {
		t.Fatal("worker increase not applied")
	}
	jobs := queue.scheduleBatch([]thumbnailSource{{virtualPath: "/Files/a.mp4", key: "test"}})
	if len(jobs) != 1 {
		t.Fatal("no job scheduled")
	}
	settings.VideosEnabled = false
	if err := service.ConfigureThumbnails(settings); err != nil {
		t.Fatal(err)
	}
	queue.mu.Lock()
	job := queue.ids[jobs[0].ID]
	state := job.view.State
	queue.mu.Unlock()
	if state != "cancelled" {
		t.Fatalf("queued job not cancelled: %s", state)
	}
	if got := queue.scheduleBatch([]thumbnailSource{{virtualPath: "/Files/b.mp4"}}); len(got) != 0 {
		t.Fatal("disabled queue accepted work")
	}
	settings.VideosEnabled = true
	settings.VideoWorkers = 1
	if err := service.ConfigureThumbnails(settings); err != nil {
		t.Fatal(err)
	}
	queue.mu.Lock()
	workers, disabled := queue.workers, queue.disabled
	queue.mu.Unlock()
	if workers != 1 || disabled {
		t.Fatal("queue not reenabled")
	}
	retried := queue.scheduleBatch([]thumbnailSource{{virtualPath: "/Files/a.mp4", key: "test"}})
	if len(retried) != 1 || retried[0].State != "settling" {
		t.Fatalf("disabled work did not retry: %#v", retried)
	}
	if !queue.cancelJob(retried[0].ID) {
		t.Fatal("manual cancellation failed")
	}
	polled := queue.scheduleBatch([]thumbnailSource{{virtualPath: "/Files/a.mp4", key: "test"}})
	if len(polled) != 1 || polled[0].State != "cancelled" {
		t.Fatalf("manual cancellation restarted: %#v", polled)
	}
	settings.VideoWorkers = 33
	if err := settings.Validate(); err == nil {
		t.Fatal("unbounded worker count allowed")
	}
}
