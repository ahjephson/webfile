package filesystem

import (
	"fmt"
	"testing"
	"time"
)

func TestFailedThumbnailHistoryIsBoundedAndRetriesRespectCapacity(t *testing.T) {
	q := newVideoThumbnailQueue(&Service{}, ThumbnailOptions{MaxQueued: 2, VideoTimeout: time.Minute})
	t.Cleanup(q.close)
	q.mu.Lock()
	q.settleDelay = time.Hour
	q.mu.Unlock()
	for i := 0; i < 80; i++ {
		source := thumbnailSource{virtualPath: fmt.Sprintf("/Files/video-%d.mp4", i), key: fmt.Sprint(i), width: 320, height: 180}
		views := q.scheduleBatch([]thumbnailSource{source})
		if len(views) != 1 {
			t.Fatal("failed history prevented fresh work")
		}
		q.mu.Lock()
		job := q.ids[views[0].ID]
		q.settling = nil
		q.mu.Unlock()
		q.finish(job, "failed", "unsupported media", source.key)
	}
	q.mu.Lock()
	count := len(q.jobs)
	q.mu.Unlock()
	if count > 50 {
		t.Fatalf("unbounded failed history: %d", count)
	}
	// Reusing failed IDs, with unchanged or changed content, must use the same
	// capacity check as fresh work.
	q.scheduleBatch([]thumbnailSource{{virtualPath: "/Files/new-1.mp4", key: "new1"}, {virtualPath: "/Files/new-2.mp4", key: "new2"}})
	q.mu.Lock()
	var failed []thumbnailSource
	for _, job := range q.jobs {
		if job.view.State == "failed" {
			job.view.UpdatedAt = time.Now().Add(-time.Hour)
			failed = append(failed, job.source)
		}
	}
	q.mu.Unlock()
	if len(failed) < 2 {
		t.Fatal("missing failed samples")
	}
	failed[1].key += "changed"
	q.scheduleBatch(failed[:2])
	q.mu.Lock()
	pending := len(q.queued) + len(q.settling)
	q.mu.Unlock()
	if pending > 2 {
		t.Fatalf("retry bypassed queue bound: %d", pending)
	}
}

func TestFailedThumbnailExpiryDoesNotRemoveRetriedJob(t *testing.T) {
	q := newVideoThumbnailQueue(&Service{}, ThumbnailOptions{MaxQueued: 2, VideoTimeout: time.Minute})
	t.Cleanup(q.close)
	q.mu.Lock()
	q.settleDelay = time.Hour
	q.mu.Unlock()
	source := thumbnailSource{virtualPath: "/Files/failure.mp4", key: "old"}
	view, err := q.schedule(source)
	if err != nil {
		t.Fatal(err)
	}
	q.mu.Lock()
	job := q.ids[view.ID]
	q.settling = nil
	q.mu.Unlock()
	q.finish(job, "failed", "failed", source.key)
	q.mu.Lock()
	finished := *job.view.FinishedAt
	q.mu.Unlock()
	source.key = "new"
	if _, err := q.schedule(source); err != nil {
		t.Fatal(err)
	}
	q.remove(job, finished)
	q.mu.Lock()
	retained := q.ids[view.ID] == job
	q.mu.Unlock()
	if !retained {
		t.Fatal("old expiry removed a retry")
	}
	q.mu.Lock()
	q.settling = nil
	q.mu.Unlock()
	q.finish(job, "failed", "failed again", source.key)
	q.mu.Lock()
	job.view.UpdatedAt = time.Now().Add(-failedThumbnailLife)
	q.trimFailuresLocked()
	retained = q.ids[view.ID] != nil
	q.mu.Unlock()
	if retained {
		t.Fatal("failed job never expired")
	}
}
