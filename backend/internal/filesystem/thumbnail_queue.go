package filesystem

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"webfile/internal/clienterror"
)

var ErrThumbnailQueueFull = errors.New("video thumbnail queue is full")

const failedThumbnailLimit = 50
const failedThumbnailLife = 5 * time.Minute

type VideoThumbnailJob struct {
	ID            string     `json:"id"`
	Path          string     `json:"path"`
	Name          string     `json:"name"`
	State         string     `json:"state"`
	QueuePosition int        `json:"queue_position,omitempty"`
	Error         string     `json:"error,omitempty"`
	Cancellable   bool       `json:"cancellable"`
	StartedAt     *time.Time `json:"started_at,omitempty"`
	UpdatedAt     time.Time  `json:"updated_at"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
}

type videoThumbnailJob struct {
	view       VideoThumbnailJob
	source     thumbnailSource
	logicalKey string
	eligibleAt time.Time
	done       chan struct{}
	cancel     context.CancelFunc
	ctx        context.Context
}

type videoThumbnailQueue struct {
	service       *Service
	workers       int
	spawned       int
	active        int
	disabled      bool
	maxQueued     int
	timeout       time.Duration
	mu            sync.Mutex
	jobs          map[string]*videoThumbnailJob
	ids           map[string]*videoThumbnailJob
	settling      []*videoThumbnailJob
	queued        []*videoThumbnailJob
	subscribers   map[chan VideoThumbnailJob]struct{}
	settleWake    chan struct{}
	wake          chan struct{}
	stop          chan struct{}
	closeOnce     sync.Once
	workerWait    sync.WaitGroup
	retryBackoff  time.Duration
	completedLife time.Duration
	settleDelay   time.Duration
}

func newVideoThumbnailQueue(service *Service, options ThumbnailOptions) *videoThumbnailQueue {
	queue := &videoThumbnailQueue{
		service: service, workers: options.VideoWorkers, spawned: options.VideoWorkers, maxQueued: options.MaxQueued, timeout: options.VideoTimeout,
		jobs: make(map[string]*videoThumbnailJob), ids: make(map[string]*videoThumbnailJob),
		subscribers: make(map[chan VideoThumbnailJob]struct{}), settleWake: make(chan struct{}, 1), wake: make(chan struct{}, 32), stop: make(chan struct{}),
		retryBackoff: 10 * time.Second, completedLife: 3 * time.Second, settleDelay: 2 * time.Second,
	}
	queue.workerWait.Add(1)
	go queue.runSettler()
	for i := 0; i < queue.workers; i++ {
		queue.workerWait.Add(1)
		go queue.runWorker()
	}
	return queue
}

func (q *videoThumbnailQueue) schedule(source thumbnailSource) (VideoThumbnailJob, error) {
	jobs := q.scheduleBatch([]thumbnailSource{source})
	if len(jobs) == 0 {
		return VideoThumbnailJob{}, ErrThumbnailQueueFull
	}
	return jobs[0], nil
}

func (q *videoThumbnailQueue) scheduleBatch(sources []thumbnailSource) []VideoThumbnailJob {
	q.mu.Lock()
	if q.disabled {
		q.mu.Unlock()
		return nil
	}
	jobs := make([]VideoThumbnailJob, 0, len(sources))
	added := 0
	for _, source := range sources {
		logicalKey := videoThumbnailLogicalKey(source)
		if existing := q.jobs[logicalKey]; existing != nil {
			needsSlot := isThumbnailTerminal(existing.view.State) && (existing.source.key != source.key || existing.view.State == "cancelled" || existing.view.State == "failed" && time.Since(existing.view.UpdatedAt) >= q.retryBackoff)
			if needsSlot && len(q.queued)+len(q.settling) >= q.maxQueued {
				continue
			}
			if existing.source.key != source.key {
				now := time.Now()
				previousState := existing.view.State
				existing.source = source
				existing.view.Path, existing.view.Name = source.virtualPath, filepath.Base(source.virtualPath)
				existing.view.Error, existing.view.FinishedAt, existing.view.UpdatedAt = "", nil, now.UTC()
				existing.eligibleAt = now.Add(q.settleDelay)
				if isThumbnailTerminal(existing.view.State) {
					existing.done = make(chan struct{})
					existing.view.State, existing.view.Cancellable, existing.view.StartedAt = "settling", true, nil
					q.settling = append(q.settling, existing)
					added++
				} else if existing.view.State == "queued" {
					q.removeQueuedLocked(existing)
					existing.view.State = "settling"
					q.settling = append(q.settling, existing)
					added++
				}
				if previousState != "settling" {
					q.publishLocked(q.snapshotLocked(existing))
				}
			} else if existing.view.State == "cancelled" || existing.view.State == "failed" && time.Since(existing.view.UpdatedAt) >= q.retryBackoff {
				now := time.Now()
				existing.done = make(chan struct{})
				existing.view.State, existing.view.Error, existing.view.Cancellable = "settling", "", true
				existing.view.StartedAt, existing.view.FinishedAt = nil, nil
				existing.view.UpdatedAt, existing.eligibleAt = now.UTC(), now.Add(q.settleDelay)
				q.settling = append(q.settling, existing)
				added++
				q.publishLocked(q.snapshotLocked(existing))
			}
			jobs = append(jobs, q.snapshotLocked(existing))
			continue
		}
		if len(q.queued)+len(q.settling) >= q.maxQueued {
			break
		}
		now := time.Now().UTC()
		job := &videoThumbnailJob{
			view: VideoThumbnailJob{
				ID: videoThumbnailJobID(logicalKey), Path: source.virtualPath, Name: filepath.Base(source.virtualPath),
				State: "settling", Cancellable: true, UpdatedAt: now,
			},
			source: source, logicalKey: logicalKey, eligibleAt: time.Now().Add(q.settleDelay), done: make(chan struct{}),
		}
		q.jobs[logicalKey], q.ids[job.view.ID] = job, job
		q.settling = append(q.settling, job)
		jobs = append(jobs, q.snapshotLocked(job))
		added++
	}
	q.mu.Unlock()
	if added > 0 {
		q.signalSettler()
	}
	return jobs
}

func (q *videoThumbnailQueue) runSettler() {
	defer q.workerWait.Done()
	janitor := time.NewTicker(30 * time.Second)
	defer janitor.Stop()
	for {
		job, wait := q.nextSettling()
		if job != nil {
			q.settle(job)
			continue
		}
		var timer <-chan time.Time
		if wait > 0 {
			timer = time.After(wait)
		}
		select {
		case <-janitor.C:
			q.mu.Lock()
			q.trimFailuresLocked()
			q.mu.Unlock()
		case <-q.settleWake:
		case <-timer:
		case <-q.stop:
			return
		}
	}
}

func (q *videoThumbnailQueue) nextSettling() (*videoThumbnailJob, time.Duration) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.settling) == 0 {
		return nil, 0
	}
	now := time.Now()
	index := -1
	var wait time.Duration
	for candidateIndex, candidate := range q.settling {
		remaining := candidate.eligibleAt.Sub(now)
		if remaining <= 0 {
			index = candidateIndex
			break
		}
		if wait == 0 || remaining < wait {
			wait = remaining
		}
	}
	if index < 0 {
		return nil, wait
	}
	job := q.settling[index]
	q.settling = append(q.settling[:index], q.settling[index+1:]...)
	job.ctx, job.cancel = context.WithCancel(context.Background())
	return job, 0
}

func (q *videoThumbnailQueue) settle(job *videoThumbnailJob) {
	q.mu.Lock()
	expected, ctx, cancel := job.source, job.ctx, job.cancel
	q.mu.Unlock()
	defer cancel()

	source, file, err := q.service.inspectThumbnailSource(ctx, expected.virtualPath, expected.width, expected.height)
	if err == nil {
		_ = file.Close()
	}
	if errors.Is(err, context.Canceled) {
		q.finish(job, "cancelled", "cancelled", "")
		return
	}
	if err != nil {
		q.discardSettling(job, err)
		return
	}
	if source.key != expected.key || source.mediaType != expected.mediaType || q.sourceChanged(job, expected) {
		q.resettle(job, expected, source)
		return
	}

	q.mu.Lock()
	job.cancel, job.ctx = nil, nil
	job.view.State, job.view.Error, job.view.Cancellable = "queued", "", true
	job.view.UpdatedAt = time.Now().UTC()
	q.queued = append(q.queued, job)
	q.publishQueuePositionsLocked()
	q.mu.Unlock()
	q.signal()
}

func (q *videoThumbnailQueue) discardSettling(job *videoThumbnailJob, err error) {
	slog.Error("thumbnail preparation failed", "id", job.view.ID, "err", err)
	q.mu.Lock()
	if q.jobs[job.logicalKey] == job {
		now := time.Now().UTC()
		job.view.State, job.view.Error, job.view.Cancellable = "failed", clienterror.Text(err), false
		job.view.UpdatedAt, job.view.FinishedAt = now, &now
		job.cancel, job.ctx = nil, nil
		delete(q.jobs, job.logicalKey)
		delete(q.ids, job.view.ID)
		close(job.done)
	}
	q.mu.Unlock()
}

func videoThumbnailLogicalKey(source thumbnailSource) string {
	return fmt.Sprintf("%s:%d:%d", CleanVirtual(source.virtualPath), source.width, source.height)
}

func videoThumbnailJobID(logicalKey string) string {
	digest := sha256.Sum256([]byte(logicalKey))
	return fmt.Sprintf("%x", digest)[:16]
}

func isThumbnailTerminal(state string) bool {
	return state == "ready" || state == "failed" || state == "cancelled"
}

func (q *videoThumbnailQueue) runWorker() {
	defer q.workerWait.Done()
	for {
		select {
		case <-q.stop:
			return
		default:
		}
		job, wait := q.next()
		if job == nil {
			var timer <-chan time.Time
			if wait > 0 {
				timer = time.After(wait)
			}
			select {
			case <-q.wake:
				continue
			case <-timer:
				continue
			case <-q.stop:
				return
			}
		}
		q.run(job)
		q.mu.Lock()
		q.active--
		q.mu.Unlock()
		q.signal()
	}
}

func (q *videoThumbnailQueue) next() (*videoThumbnailJob, time.Duration) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.queued) == 0 || q.active >= q.workers {
		return nil, 0
	}
	job := q.queued[0]
	q.active++
	q.queued = q.queued[1:]
	q.publishQueuePositionsLocked()
	ctx, cancel := context.WithCancel(context.Background())
	job.ctx, job.cancel = ctx, cancel
	started := time.Now().UTC()
	job.view.State, job.view.StartedAt, job.view.UpdatedAt = "probing", &started, started
	q.publishLocked(q.snapshotLocked(job))
	return job, 0
}

func (q *videoThumbnailQueue) run(job *videoThumbnailJob) {
	q.mu.Lock()
	cancel := job.cancel
	q.mu.Unlock()
	ctx := job.ctx
	defer cancel()
	q.mu.Lock()
	expected := job.source
	q.mu.Unlock()

	source, file, err := q.service.inspectThumbnailSource(ctx, expected.virtualPath, expected.width, expected.height)
	if err == nil {
		_ = file.Close()
		if source.key != expected.key || source.mediaType != expected.mediaType || q.sourceChanged(job, expected) {
			q.resettle(job, expected, source)
			return
		}
	}
	if err == nil {
		q.setState(job, "generating", "")
		var data []byte
		q.mu.Lock()
		timeout := q.timeout
		q.mu.Unlock()
		generator := q.service.videoThumbnails
		generator.threads = q.service.ThumbnailSettings().VideoFFmpegThreads
		generationCtx, generationCancel := context.WithTimeout(ctx, timeout)
		data, err = generator.generate(generationCtx, source.absolute, source.width, source.height)
		generationCancel()
		latest, latestFile, inspectErr := q.service.inspectThumbnailSource(ctx, source.virtualPath, source.width, source.height)
		if inspectErr == nil {
			_ = latestFile.Close()
		}
		if inspectErr == nil && (latest.key != source.key || q.sourceChanged(job, expected)) {
			q.resettle(job, expected, latest)
			return
		}
		if err == nil {
			if inspectErr != nil {
				err = inspectErr
			}
		} else if q.sourceChanged(job, expected) {
			q.resettle(job, expected, latest)
			return
		}
		if err == nil {
			q.setState(job, "publishing", "")
			_, err = q.service.storeThumbnail(ctx, source, data)
		}
		if err == nil && q.sourceChanged(job, expected) {
			q.resettle(job, expected, thumbnailSource{})
			return
		}
	}
	if err != nil {
		state := "failed"
		if errors.Is(err, context.Canceled) {
			state = "cancelled"
		}
		slog.Error("thumbnail task failed", "id", job.view.ID, "err", err)
		q.finish(job, state, clienterror.Text(err), expected.key)
		return
	}
	q.finish(job, "ready", "", expected.key)
}

func (q *videoThumbnailQueue) sourceChanged(job *videoThumbnailJob, expected thumbnailSource) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return job.source.key != expected.key
}

func (q *videoThumbnailQueue) resettle(job *videoThumbnailJob, expected, source thumbnailSource) {
	q.mu.Lock()
	if job.source.key == expected.key && source.key != "" {
		job.source = source
	}
	job.cancel, job.ctx = nil, nil
	job.view.State, job.view.Error, job.view.Cancellable = "settling", "", true
	job.view.StartedAt, job.view.FinishedAt = nil, nil
	job.view.UpdatedAt, job.eligibleAt = time.Now().UTC(), time.Now().Add(q.settleDelay)
	q.settling = append(q.settling, job)
	q.publishLocked(q.snapshotLocked(job))
	q.mu.Unlock()
	q.signalSettler()
}

func (q *videoThumbnailQueue) setState(job *videoThumbnailJob, state, detail string) {
	q.mu.Lock()
	job.view.State, job.view.Error, job.view.UpdatedAt = state, detail, time.Now().UTC()
	q.publishLocked(q.snapshotLocked(job))
	q.mu.Unlock()
}

func (q *videoThumbnailQueue) finish(job *videoThumbnailJob, state, detail, expectedKey string) {
	q.mu.Lock()
	if expectedKey != "" && state != "cancelled" && job.source.key != expectedKey {
		job.cancel, job.ctx = nil, nil
		job.view.State, job.view.Error, job.view.Cancellable = "settling", "", true
		job.view.StartedAt, job.view.FinishedAt, job.view.UpdatedAt = nil, nil, time.Now().UTC()
		job.eligibleAt = time.Now().Add(q.settleDelay)
		q.settling = append(q.settling, job)
		q.publishLocked(q.snapshotLocked(job))
		q.mu.Unlock()
		q.signalSettler()
		return
	}
	now := time.Now().UTC()
	job.view.State, job.view.Error, job.view.UpdatedAt, job.view.FinishedAt, job.view.Cancellable = state, detail, now, &now, false
	job.cancel = nil
	close(job.done)
	q.publishLocked(q.snapshotLocked(job))
	if state == "failed" {
		q.trimFailuresLocked()
	}
	q.mu.Unlock()
	if state == "ready" || state == "cancelled" {
		time.AfterFunc(q.completedLife, func() { q.remove(job, now) })
	}
}

func (q *videoThumbnailQueue) trimFailuresLocked() {
	failed := make([]*videoThumbnailJob, 0, failedThumbnailLimit+1)
	for _, job := range q.jobs {
		if job.view.State != "failed" {
			continue
		}
		if time.Since(job.view.UpdatedAt) >= failedThumbnailLife {
			delete(q.jobs, job.logicalKey)
			delete(q.ids, job.view.ID)
		} else {
			failed = append(failed, job)
		}
	}
	if len(failed) <= failedThumbnailLimit {
		return
	}
	sort.Slice(failed, func(i, j int) bool { return failed[i].view.UpdatedAt.Before(failed[j].view.UpdatedAt) })
	for _, job := range failed[:len(failed)-failedThumbnailLimit] {
		delete(q.jobs, job.logicalKey)
		delete(q.ids, job.view.ID)
	}
}

func (q *videoThumbnailQueue) remove(job *videoThumbnailJob, finishedAt time.Time) {
	q.mu.Lock()
	if q.jobs[job.logicalKey] == job &&
		job.view.FinishedAt != nil && job.view.FinishedAt.Equal(finishedAt) &&
		isThumbnailTerminal(job.view.State) {
		delete(q.jobs, job.logicalKey)
		delete(q.ids, job.view.ID)
	}
	q.mu.Unlock()
}

func (q *videoThumbnailQueue) wait(ctx context.Context, key string) error {
	q.mu.Lock()
	var job *videoThumbnailJob
	for _, candidate := range q.jobs {
		if candidate.source.key == key {
			job = candidate
			break
		}
	}
	q.mu.Unlock()
	if job == nil {
		return errors.New("thumbnail job is unavailable")
	}
	select {
	case <-job.done:
		if job.view.State != "ready" {
			return errors.New(job.view.Error)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (q *videoThumbnailQueue) cancelJob(id string) bool {
	q.mu.Lock()
	job := q.ids[id]
	if job == nil {
		q.mu.Unlock()
		return false
	}
	if job.view.State == "failed" {
		delete(q.jobs, job.logicalKey)
		delete(q.ids, job.view.ID)
		q.mu.Unlock()
		return true
	}
	if !job.view.Cancellable {
		q.mu.Unlock()
		return false
	}
	if job.view.State == "queued" {
		q.removeQueuedLocked(job)
		q.mu.Unlock()
		q.finish(job, "cancelled", "cancelled", "")
		return true
	}
	if job.view.State == "settling" {
		for index, settling := range q.settling {
			if settling == job {
				q.settling = append(q.settling[:index], q.settling[index+1:]...)
				q.mu.Unlock()
				q.finish(job, "cancelled", "cancelled", "")
				return true
			}
		}
		cancel := job.cancel
		q.mu.Unlock()
		if cancel != nil {
			cancel()
			return true
		}
		return false
	}
	cancel := job.cancel
	q.mu.Unlock()
	if cancel != nil {
		cancel()
		return true
	}
	return false
}

func (q *videoThumbnailQueue) cancelPath(ctx context.Context, virtualPath string) {
	virtualPath = CleanVirtual(virtualPath)
	prefix := virtualPath + "/"
	q.mu.Lock()
	jobs := make([]*videoThumbnailJob, 0)
	for _, job := range q.ids {
		if job.view.Path == virtualPath || strings.HasPrefix(job.view.Path, prefix) {
			jobs = append(jobs, job)
		}
	}
	q.mu.Unlock()
	for _, job := range jobs {
		q.cancelJob(job.view.ID)
	}
	for _, job := range jobs {
		select {
		case <-job.done:
		case <-ctx.Done():
			return
		}
	}
}

func (q *videoThumbnailQueue) snapshot() []VideoThumbnailJob {
	q.mu.Lock()
	defer q.mu.Unlock()
	jobs := make([]VideoThumbnailJob, 0, len(q.jobs))
	for _, job := range q.jobs {
		if job.view.State == "settling" {
			continue
		}
		jobs = append(jobs, q.snapshotLocked(job))
	}
	sort.Slice(jobs, func(i, j int) bool { return jobs[i].UpdatedAt.After(jobs[j].UpdatedAt) })
	return jobs
}

func (q *videoThumbnailQueue) snapshotLocked(job *videoThumbnailJob) VideoThumbnailJob {
	view := job.view
	if view.State == "queued" {
		for index, queued := range q.queued {
			if queued == job {
				view.QueuePosition = index + 1
				break
			}
		}
	}
	return view
}

func (q *videoThumbnailQueue) subscribe() (<-chan VideoThumbnailJob, func()) {
	q.mu.Lock()
	channel := make(chan VideoThumbnailJob, 16)
	q.subscribers[channel] = struct{}{}
	q.mu.Unlock()
	return channel, func() {
		q.mu.Lock()
		if _, ok := q.subscribers[channel]; ok {
			delete(q.subscribers, channel)
			close(channel)
		}
		q.mu.Unlock()
	}
}

func (q *videoThumbnailQueue) publishLocked(job VideoThumbnailJob) {
	for subscriber := range q.subscribers {
		select {
		case subscriber <- job:
		default:
		}
	}
}

func (q *videoThumbnailQueue) publishQueuePositionsLocked() {
	for _, job := range q.queued {
		q.publishLocked(q.snapshotLocked(job))
	}
}

func (q *videoThumbnailQueue) removeQueuedLocked(job *videoThumbnailJob) {
	for index, queued := range q.queued {
		if queued == job {
			q.queued = append(q.queued[:index], q.queued[index+1:]...)
			q.publishQueuePositionsLocked()
			return
		}
	}
}

func (q *videoThumbnailQueue) signalSettler() {
	select {
	case q.settleWake <- struct{}{}:
	default:
	}
}

func (q *videoThumbnailQueue) signal() {
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

func (q *videoThumbnailQueue) close() {
	q.closeOnce.Do(func() {
		close(q.stop)
		q.mu.Lock()
		queued := append([]*videoThumbnailJob(nil), q.queued...)
		queued = append(queued, q.settling...)
		q.queued = nil
		q.settling = nil
		for _, job := range q.jobs {
			if job.cancel != nil {
				job.cancel()
			}
		}
		q.mu.Unlock()
		for _, job := range queued {
			q.finish(job, "cancelled", "service stopped", "")
		}
		q.workerWait.Wait()
	})
}

func (s *Service) VideoThumbnailJobs() []VideoThumbnailJob { return s.videoQueue.snapshot() }
func (s *Service) CancelVideoThumbnailJob(id string) bool  { return s.videoQueue.cancelJob(id) }
func (s *Service) SubscribeVideoThumbnails() (<-chan VideoThumbnailJob, func()) {
	return s.videoQueue.subscribe()
}
