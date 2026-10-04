package filesystem

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var ErrThumbnailQueueFull = errors.New("video thumbnail queue is full")

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
	view   VideoThumbnailJob
	source thumbnailSource
	done   chan struct{}
	cancel context.CancelFunc
	ctx    context.Context
}

type videoThumbnailQueue struct {
	service       *Service
	workers       int
	maxQueued     int
	timeout       time.Duration
	mu            sync.Mutex
	jobs          map[string]*videoThumbnailJob
	ids           map[string]*videoThumbnailJob
	queued        []*videoThumbnailJob
	subscribers   map[chan VideoThumbnailJob]struct{}
	wake          chan struct{}
	stop          chan struct{}
	closeOnce     sync.Once
	workerWait    sync.WaitGroup
	retryBackoff  time.Duration
	completedLife time.Duration
}

func newVideoThumbnailQueue(service *Service, options ThumbnailOptions) *videoThumbnailQueue {
	queue := &videoThumbnailQueue{
		service: service, workers: options.VideoWorkers, maxQueued: options.MaxQueued, timeout: options.VideoTimeout,
		jobs: make(map[string]*videoThumbnailJob), ids: make(map[string]*videoThumbnailJob),
		subscribers: make(map[chan VideoThumbnailJob]struct{}), wake: make(chan struct{}, options.VideoWorkers), stop: make(chan struct{}),
		retryBackoff: 10 * time.Second, completedLife: 3 * time.Second,
	}
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
	jobs := make([]VideoThumbnailJob, 0, len(sources))
	added := 0
	for _, source := range sources {
		if existing := q.jobs[source.key]; existing != nil {
			if existing.view.State != "failed" || time.Since(existing.view.UpdatedAt) < q.retryBackoff {
				jobs = append(jobs, q.snapshotLocked(existing))
				continue
			}
			delete(q.jobs, source.key)
			delete(q.ids, existing.view.ID)
		}
		if len(q.queued) >= q.maxQueued {
			break
		}
		now := time.Now().UTC()
		job := &videoThumbnailJob{
			view: VideoThumbnailJob{
				ID: source.key[:16], Path: source.virtualPath, Name: filepath.Base(source.virtualPath),
				State: "queued", Cancellable: true, UpdatedAt: now,
			},
			source: source,
			done:   make(chan struct{}),
		}
		q.jobs[source.key], q.ids[job.view.ID] = job, job
		q.queued = append(q.queued, job)
		jobs = append(jobs, q.snapshotLocked(job))
		added++
	}
	q.publishQueuePositionsLocked()
	q.mu.Unlock()
	for index := 0; index < added && index < q.workers; index++ {
		q.signal()
	}
	return jobs
}

func (q *videoThumbnailQueue) runWorker() {
	defer q.workerWait.Done()
	for {
		select {
		case <-q.stop:
			return
		default:
		}
		job := q.next()
		if job == nil {
			select {
			case <-q.wake:
				continue
			case <-q.stop:
				return
			}
		}
		q.run(job)
	}
}

func (q *videoThumbnailQueue) next() *videoThumbnailJob {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.queued) == 0 {
		return nil
	}
	job := q.queued[0]
	q.queued = q.queued[1:]
	q.publishQueuePositionsLocked()
	ctx, cancel := context.WithTimeout(context.Background(), q.timeout)
	job.ctx, job.cancel = ctx, cancel
	now := time.Now().UTC()
	job.view.State, job.view.StartedAt, job.view.UpdatedAt = "probing", &now, now
	q.publishLocked(q.snapshotLocked(job))
	return job
}

func (q *videoThumbnailQueue) run(job *videoThumbnailJob) {
	q.mu.Lock()
	cancel := job.cancel
	q.mu.Unlock()
	ctx := job.ctx
	defer cancel()

	source, file, err := q.service.inspectThumbnailSource(ctx, job.source.virtualPath, job.source.width, job.source.height)
	if err == nil {
		_ = file.Close()
		if source.key != job.source.key || source.mediaType != job.source.mediaType {
			err = errors.New("source changed while thumbnail was queued")
		}
	}
	if err == nil {
		q.setState(job, "generating", "")
		var data []byte
		data, err = q.service.videoThumbnails.generate(ctx, source.absolute, source.width, source.height)
		if err == nil {
			q.setState(job, "publishing", "")
			_, err = q.service.storeThumbnail(ctx, source, data)
		}
	}
	if err != nil {
		state := "failed"
		if errors.Is(err, context.Canceled) {
			state = "cancelled"
		}
		q.finish(job, state, err.Error())
		return
	}
	q.finish(job, "ready", "")
}

func (q *videoThumbnailQueue) setState(job *videoThumbnailJob, state, detail string) {
	q.mu.Lock()
	job.view.State, job.view.Error, job.view.UpdatedAt = state, detail, time.Now().UTC()
	q.publishLocked(q.snapshotLocked(job))
	q.mu.Unlock()
}

func (q *videoThumbnailQueue) finish(job *videoThumbnailJob, state, detail string) {
	q.mu.Lock()
	now := time.Now().UTC()
	job.view.State, job.view.Error, job.view.UpdatedAt, job.view.FinishedAt, job.view.Cancellable = state, detail, now, &now, false
	job.cancel = nil
	close(job.done)
	q.publishLocked(q.snapshotLocked(job))
	q.mu.Unlock()
	if state == "ready" || state == "cancelled" {
		time.AfterFunc(q.completedLife, func() { q.remove(job) })
	}
}

func (q *videoThumbnailQueue) remove(job *videoThumbnailJob) {
	q.mu.Lock()
	if q.jobs[job.source.key] == job {
		delete(q.jobs, job.source.key)
		delete(q.ids, job.view.ID)
	}
	q.mu.Unlock()
}

func (q *videoThumbnailQueue) wait(ctx context.Context, key string) error {
	q.mu.Lock()
	job := q.jobs[key]
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
	if job == nil || !job.view.Cancellable {
		q.mu.Unlock()
		return false
	}
	if job.view.State == "queued" {
		for index, queued := range q.queued {
			if queued == job {
				q.queued = append(q.queued[:index], q.queued[index+1:]...)
				break
			}
		}
		q.publishQueuePositionsLocked()
		q.mu.Unlock()
		q.finish(job, "cancelled", "cancelled")
		return true
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
		q.queued = nil
		for _, job := range q.jobs {
			if job.cancel != nil {
				job.cancel()
			}
		}
		q.mu.Unlock()
		for _, job := range queued {
			q.finish(job, "cancelled", "service stopped")
		}
		q.workerWait.Wait()
	})
}

func (s *Service) VideoThumbnailJobs() []VideoThumbnailJob { return s.videoQueue.snapshot() }
func (s *Service) CancelVideoThumbnailJob(id string) bool  { return s.videoQueue.cancelJob(id) }
func (s *Service) SubscribeVideoThumbnails() (<-chan VideoThumbnailJob, func()) {
	return s.videoQueue.subscribe()
}
