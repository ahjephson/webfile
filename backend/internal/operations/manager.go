package operations

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"sort"
	"sync"
	"time"

	"webfile/internal/clienterror"
	"webfile/internal/taskcontrol"
)

type State string

const (
	StateQueued        State = "queued"
	StatePreparing     State = "preparing"
	StateAwaitingInput State = "awaiting_input"
	StateRunning       State = "running"
	StateCancelling    State = "cancelling"
	StateCleaningUp    State = "cleaning_up"
	StateCompleted     State = "completed"
	StateFailed        State = "failed"
	StateCancelled     State = "cancelled"
	StateInterrupted   State = "interrupted"
)

type ProgressUnit string

const (
	ProgressUnitBytes   ProgressUnit = "bytes"
	ProgressUnitItems   ProgressUnit = "items"
	ProgressUnitPercent ProgressUnit = "percent"
)

type Operation struct {
	UndoData       json.RawMessage `json:"-"`
	UndoAvailable  bool            `json:"undo_available,omitempty"`
	UndoReason     string          `json:"undo_reason,omitempty"`
	Paused         bool            `json:"paused"`
	Input          json.RawMessage `json:"input,omitempty"`
	ID             string          `json:"id"`
	AttemptID      string          `json:"attempt_id,omitempty"`
	Kind           string          `json:"kind"`
	Sources        []string        `json:"sources,omitempty"`
	Destination    string          `json:"destination,omitempty"`
	State          State           `json:"state"`
	Phase          string          `json:"phase,omitempty"`
	Message        string          `json:"message,omitempty"`
	Error          string          `json:"error,omitempty"`
	ProgressDone   int64           `json:"progress_done"`
	ProgressTotal  int64           `json:"progress_total"`
	ProgressUnit   ProgressUnit    `json:"progress_unit,omitempty"`
	BytesDone      int64           `json:"bytes_done"`
	BytesTotal     int64           `json:"bytes_total"`
	BytesPerSecond *int64          `json:"bytes_per_second,omitempty"`
	ItemsPerSecond *float64        `json:"items_per_second,omitempty"`
	ETASeconds     *int64          `json:"eta_seconds,omitempty"`
	ItemsDone      int64           `json:"items_done"`
	ItemsTotal     int64           `json:"items_total"`
	QueuePosition  int             `json:"queue_position,omitempty"`
	Cancellable    bool            `json:"cancellable"`
	CleanupPolicy  string          `json:"cleanup_policy,omitempty"`
	CleanupError   string          `json:"cleanup_error,omitempty"`
	StartedAt      time.Time       `json:"started_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	FinishedAt     *time.Time      `json:"finished_at,omitempty"`
}

type ProgressUpdate struct {
	Phase         string
	Message       string
	ProgressDone  int64
	ProgressTotal int64
	ProgressUnit  ProgressUnit
	BytesDone     int64
	BytesTotal    int64
	ItemsDone     int64
	ItemsTotal    int64
}

type Reporter struct {
	manager *Manager
	id      string
	ctx     context.Context
}

var ErrAwaitingInput = errors.New("task awaiting input")

func (r Reporter) SetInput(input any) error {
	data, err := json.Marshal(input)
	if err != nil {
		return err
	}
	r.manager.mu.Lock()
	defer r.manager.mu.Unlock()
	if rec := r.manager.records[r.id]; rec != nil {
		rec.op.Input = data
	}
	return nil
}

func (r Reporter) ID() string {
	return r.id
}

func (r Reporter) SetState(state State, phase, message string) {
	if r.ctx != nil && taskcontrol.Wait(r.ctx) != nil {
		return
	}
	r.manager.setStatus(r.id, state, phase, message)
}

func (r Reporter) Update(bytesDone, bytesTotal, itemsDone, itemsTotal int64, message string) {
	r.UpdatePhase("", bytesDone, bytesTotal, itemsDone, itemsTotal, message)
}

func (r Reporter) UpdatePhase(phase string, bytesDone, bytesTotal, itemsDone, itemsTotal int64, message string) {
	done, total, unit := primaryProgress(bytesDone, bytesTotal, itemsDone, itemsTotal)
	r.Report(ProgressUpdate{
		Phase: phase, Message: message,
		ProgressDone: done, ProgressTotal: total, ProgressUnit: unit,
		BytesDone: bytesDone, BytesTotal: bytesTotal,
		ItemsDone: itemsDone, ItemsTotal: itemsTotal,
	})
}

func (r Reporter) Report(update ProgressUpdate) {
	if r.ctx != nil && taskcontrol.Wait(r.ctx) != nil {
		return
	}
	r.manager.updateProgress(r.id, update)
}

func primaryProgress(bytesDone, bytesTotal, itemsDone, itemsTotal int64) (int64, int64, ProgressUnit) {
	if bytesTotal > 0 {
		return bytesDone, bytesTotal, ProgressUnitBytes
	}
	if itemsTotal > 0 {
		return itemsDone, itemsTotal, ProgressUnitItems
	}
	return 0, 0, ""
}

type RunFunc func(context.Context, Reporter) error
type CleanupFunc func(context.Context, Operation) error

type TaskOptions struct {
	InputHandler  func(json.RawMessage) error
	Cancellable   bool
	CleanupPolicy string
	Cleanup       CleanupFunc
}

type record struct {
	externalProgressAt time.Time
	control            *taskcontrol.Control
	inputHandler       func(json.RawMessage) error
	op                 Operation
	run                RunFunc
	cleanup            CleanupFunc
	cancel             context.CancelFunc
	external           bool
	clientFailure      bool
	lastBroadcast      time.Time
	lastPersistence    time.Time
	rateSamples        []transferRateSample
	rateUnit           ProgressUnit
	rateHeartbeat      bool
}

type Manager struct {
	historyCleanup func(Operation) error
	mu             sync.Mutex
	records        map[string]*record
	subs           map[chan Operation]struct{}
	queue          []string
	store          *Store
	maxKeep        int
	maxConcurrent  int
	running        int
	runningKinds   map[string]int
	closed         bool
	wg             sync.WaitGroup
}

func New(maxKeep int) *Manager {
	return newManager(nil, maxKeep, 2)
}

func Open(databasePath string, maxKeep, maxConcurrent int) (*Manager, error) {
	store, err := OpenStore(databasePath)
	if err != nil {
		return nil, err
	}
	manager := newManager(store, maxKeep, maxConcurrent)
	stored, err := store.listForRecovery(manager.maxKeep)
	if err != nil {
		_ = store.Close()
		return nil, err
	}
	now := time.Now().UTC()
	for _, operation := range stored {
		if operation.Error != clienterror.Stored(operation.Error) || operation.CleanupError != clienterror.Stored(operation.CleanupError) {
			slog.Error("legacy task failure", "id", operation.ID, "err", operation.Error, "cleanup_err", operation.CleanupError)
			operation.Error = clienterror.Stored(operation.Error)
			if operation.CleanupError != "" {
				operation.CleanupError = string(clienterror.Cleanup)
			}
		}
		if activeState(operation.State) {
			operation.Paused = false
			operation.Input = nil
			operation.State = StateInterrupted
			operation.Phase = "interrupted"
			operation.Message = "Interrupted by worker restart"
			operation.Cancellable = false
			operation.UpdatedAt = now
			operation.FinishedAt = timePointer(now)
			if err := store.Upsert(operation, true); err != nil {
				_ = store.Close()
				return nil, err
			}
		}
		manager.records[operation.ID] = &record{op: operation, lastPersistence: now}
	}
	manager.trimLocked()
	return manager, nil
}

func newManager(store *Store, maxKeep, maxConcurrent int) *Manager {
	if maxKeep <= 0 {
		maxKeep = 50
	}
	if maxConcurrent <= 0 {
		maxConcurrent = 2
	}
	return &Manager{
		records:       make(map[string]*record),
		subs:          make(map[chan Operation]struct{}),
		store:         store,
		maxKeep:       maxKeep,
		maxConcurrent: maxConcurrent,
		runningKinds:  make(map[string]int),
	}
}

func (m *Manager) Start(kind string, sources []string, destination string, run RunFunc) Operation {
	return m.StartTask(kind, sources, destination, TaskOptions{
		Cancellable:   true,
		CleanupPolicy: "Incomplete output is removed; completed items are retained.",
	}, run)
}

func (m *Manager) StartTask(kind string, sources []string, destination string, options TaskOptions, run RunFunc) Operation {
	id := newID()
	now := time.Now().UTC()
	op := Operation{
		ID:            id,
		Kind:          kind,
		Sources:       append([]string(nil), sources...),
		Destination:   destination,
		State:         StateQueued,
		Phase:         "queued",
		Message:       "Queued",
		Cancellable:   options.Cancellable,
		CleanupPolicy: options.CleanupPolicy,
		StartedAt:     now,
		UpdatedAt:     now,
	}

	m.mu.Lock()
	if m.closed {
		op.State = StateFailed
		op.Error = string(clienterror.Unavailable)
		op.Cancellable = false
		op.FinishedAt = timePointer(now)
		m.mu.Unlock()
		return op
	}
	rec := &record{op: op, run: run, cleanup: options.Cleanup, inputHandler: options.InputHandler, control: taskcontrol.New()}
	m.records[id] = rec
	m.queue = append(m.queue, id)
	m.refreshQueuePositionsLocked()
	m.persistLocked(rec, true)
	m.trimLocked()
	m.broadcastLocked(rec.op)
	m.scheduleLocked()
	result := cloneOperation(rec.op)
	m.mu.Unlock()
	return result
}

func (m *Manager) scheduleLocked() {
	for !m.closed && m.running < m.maxConcurrent {
		queueIndex := -1
		for index, id := range m.queue {
			rec := m.records[id]
			if rec != nil && rec.op.State == StateQueued && !rec.op.Paused && rec.run != nil && m.runningKinds[rec.op.Kind] == 0 {
				queueIndex = index
				break
			}
		}
		if queueIndex < 0 {
			return
		}
		id := m.queue[queueIndex]
		m.queue = append(m.queue[:queueIndex], m.queue[queueIndex+1:]...)
		rec := m.records[id]
		rec.op.QueuePosition = 0
		ctx, cancel := context.WithCancel(context.Background())
		ctx = taskcontrol.With(ctx, rec.control)
		rec.cancel = cancel
		rec.op.State = StateRunning
		rec.op.Phase = "running"
		rec.op.Message = "Starting"
		rec.op.UpdatedAt = time.Now().UTC()
		m.running++
		m.runningKinds[rec.op.Kind]++
		m.persistLocked(rec, true)
		m.broadcastLocked(rec.op)
		m.refreshQueuePositionsLocked()
		m.wg.Add(1)
		go m.execute(ctx, id, rec.op.Kind, rec.run)
	}
}

func (m *Manager) execute(ctx context.Context, id, kind string, run RunFunc) {
	defer m.wg.Done()
	err := taskError(func() error {
		if err := taskcontrol.Wait(ctx); err != nil {
			return err
		}
		if err := run(ctx, Reporter{manager: m, id: id, ctx: ctx}); err != nil {
			return err
		}
		return taskcontrol.Wait(ctx)
	})

	m.mu.Lock()
	rec := m.records[id]
	if rec == nil {
		m.running--
		m.runningKinds[kind]--
		m.scheduleLocked()
		m.mu.Unlock()
		return
	}
	cancelled := errors.Is(err, context.Canceled) || rec.op.State == StateCancelling || ctx.Err() != nil
	if !cancelled && errors.Is(err, ErrAwaitingInput) && rec.inputHandler != nil && len(rec.op.Input) > 0 {
		if rec.cancel != nil {
			rec.cancel()
		}
		rec.cancel = nil
		rec.op.State = StateAwaitingInput
		rec.op.Phase = "conflict"
		rec.op.Message = "Waiting for your decision"
		rec.op.UpdatedAt = time.Now().UTC()
		resetTransferRate(rec)
		m.persistLocked(rec, true)
		m.broadcastLocked(rec.op)
		m.running--
		m.runningKinds[kind]--
		m.scheduleLocked()
		m.mu.Unlock()
		return
	}
	cleanup := rec.cleanup
	if cancelled && cleanup != nil {
		rec.op.State = StateCleaningUp
		rec.op.Phase = "cleaning_up"
		rec.op.Message = "Cleaning up"
		resetTransferRate(rec)
		rec.op.UpdatedAt = time.Now().UTC()
		rec.op.Cancellable = false
		rec.cancel = nil
		snapshot := cloneOperation(rec.op)
		m.persistLocked(rec, true)
		m.broadcastLocked(rec.op)
		m.mu.Unlock()

		cleanupCtx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		cleanupErr := taskError(func() error { return cleanup(cleanupCtx, snapshot) })
		cancel()

		m.mu.Lock()
		rec = m.records[id]
		if rec != nil && cleanupErr != nil {
			slog.Error("task cleanup failed", "id", id, "kind", kind, "err", cleanupErr)
			rec.op.CleanupError = string(clienterror.Cleanup)
			err = cleanupErr
			cancelled = false
		}
	}
	if rec != nil {
		rec.op.Paused = false
		rec.op.Input = nil
		now := time.Now().UTC()
		rec.op.UpdatedAt = now
		rec.op.FinishedAt = timePointer(now)
		rec.op.Cancellable = false
		resetTransferRate(rec)
		rec.cancel = nil
		switch {
		case cancelled:
			rec.op.State = StateCancelled
			rec.op.Phase = "cancelled"
			rec.op.Message = "Cancelled"
		case err != nil:
			rec.op.State = StateFailed
			rec.op.Phase = "failed"
			slog.Error("task failed", "id", id, "kind", kind, "err", err)
			rec.op.Error = clienterror.Text(err)
		default:
			rec.op.State = StateCompleted
			rec.op.Phase = "completed"
			if rec.op.Message == "" || rec.op.Message == "Starting" {
				rec.op.Message = "Completed"
			}
		}
		rec.op.UndoAvailable = rec.op.State == StateCompleted && len(rec.op.UndoData) > 0
		if rec.op.State != StateCompleted {
			rec.op.UndoData = nil
			rec.op.UndoReason = ""
		}
		m.persistLocked(rec, true)
		m.broadcastLocked(rec.op)
	}
	m.running--
	m.runningKinds[kind]--
	m.trimLocked()
	m.scheduleLocked()
	m.mu.Unlock()
}

// Keep unexpected handler failures inside the task lifecycle so they cannot
// strand a task, consume a worker slot forever, or terminate the worker process.
func taskError(run func() error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			slog.Error("task handler panicked", "panic", recovered)
			err = errors.New("task handler failed unexpectedly")
		}
	}()
	return run()
}

func (m *Manager) UpsertExternal(id, kind string, sources []string, destination string, state State, bytesDone, bytesTotal, itemsDone, itemsTotal int64, message, errText string, requestedAttempt ...string) Operation {
	m.mu.Lock()
	defer m.mu.Unlock()
	attempt := newID()
	if len(requestedAttempt) > 0 && requestedAttempt[0] != "" {
		attempt = requestedAttempt[0]
	}
	return m.upsertExternalLocked(id, kind, sources, destination, state, bytesDone, bytesTotal, itemsDone, itemsTotal, message, errText, attempt)
}

// QueueExternal registers browser-driven work without occupying a worker slot.
// An existing active attempt must not be replaced by another selected batch.
func (m *Manager) QueueExternal(id, kind string, sources []string, destination string, bytesDone, bytesTotal int64, attempt string) (Operation, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if rec := m.records[id]; rec != nil && activeState(rec.op.State) {
		return cloneOperation(rec.op), false
	}
	return m.upsertExternalLocked(id, kind, sources, destination, StateQueued, bytesDone, bytesTotal, 0, 1, "Queued", "", attempt), true
}

// StartExternal fences the transition against cancellation and newer attempts.
func (m *Manager) StartExternal(id, attempt, message string) (Operation, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec := m.records[id]
	if rec == nil || !rec.external || attempt == "" || rec.op.AttemptID != attempt || rec.op.State != StateQueued {
		return Operation{}, false
	}
	op := rec.op
	return m.upsertExternalLocked(id, op.Kind, op.Sources, op.Destination, StateRunning, op.BytesDone, op.BytesTotal, op.ItemsDone, op.ItemsTotal, message, "", attempt), true
}

func (m *Manager) upsertExternalLocked(id, kind string, sources []string, destination string, state State, bytesDone, bytesTotal, itemsDone, itemsTotal int64, message, errText, attempt string) Operation {
	now := time.Now().UTC()
	rec := m.records[id]
	previousState := State("")
	if rec == nil {
		rec = &record{op: Operation{ID: id, Kind: kind, Sources: append([]string(nil), sources...), Destination: destination, StartedAt: now}, external: true}
		m.records[id] = rec
	} else {
		previousState = rec.op.State
	}
	rec.external = true
	rec.clientFailure = false
	if rec.op.AttemptID != attempt || previousState != state {
		resetTransferRate(rec)
	}
	rec.op.AttemptID = attempt
	rec.op.Kind = kind
	rec.op.Sources = append([]string(nil), sources...)
	rec.op.Destination = destination
	rec.op.State = state
	rec.op.Phase = string(state)
	rec.op.BytesDone = bytesDone
	rec.op.BytesTotal = bytesTotal
	rec.op.ProgressDone, rec.op.ProgressTotal, rec.op.ProgressUnit = primaryProgress(bytesDone, bytesTotal, itemsDone, itemsTotal)
	rec.op.ItemsDone = itemsDone
	rec.op.ItemsTotal = itemsTotal
	rec.op.Message = message
	if errText != "" {
		slog.Error("external task failed", "id", id, "kind", kind, "err", errText)
	}
	rec.op.Error = clienterror.Stored(errText)
	rec.op.UpdatedAt = now
	rec.externalProgressAt = now
	rec.op.Cancellable = rec.cancel != nil && activeState(state)
	if terminalState(state) {
		rec.op.Paused = false
		rec.op.FinishedAt = timePointer(now)
		rec.op.Cancellable = false
	} else {
		rec.op.FinishedAt = nil
	}
	m.updateTaskMetricsLocked(rec, now)
	m.persistLocked(rec, previousState != state)
	m.trimLocked()
	m.broadcastLocked(rec.op)
	return cloneOperation(rec.op)
}

// AdvanceExternal cannot revive a stopped upload or overwrite a newer retry.
// Confirmed publication may still win a race with a client reporting a lost response.
func (m *Manager) AdvanceExternal(op Operation, attempt string) (Operation, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec := m.records[op.ID]
	if rec == nil || !rec.external {
		return Operation{}, false
	}
	if rec.op.AttemptID != attempt || (rec.op.State != StateRunning && !(rec.clientFailure && op.State == StateCompleted)) {
		return cloneOperation(rec.op), false
	}
	return m.upsertExternalLocked(op.ID, op.Kind, op.Sources, op.Destination, op.State, op.BytesDone, op.BytesTotal, op.ItemsDone, op.ItemsTotal, op.Message, op.Error, attempt), true
}

// ReportExternalFailure handles failures outside the worker, including proxy
// rejections. stop must preserve staging and must not call back into Manager.
func (m *Manager) ReportExternalFailure(id, attempt string, stop func(string)) (Operation, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec := m.records[id]
	if id == "" && attempt != "" {
		for _, candidate := range m.records {
			if candidate.op.AttemptID == attempt {
				rec = candidate
				break
			}
		}
	}
	if rec == nil || !rec.external || rec.op.Kind != "upload" {
		return Operation{}, false
	}
	if attempt != "" && rec.op.AttemptID == attempt && (rec.op.State == StateRunning || rec.op.State == StateQueued) {
		if stop != nil {
			stop(rec.op.ID)
		}
		m.failExternalLocked(rec, clienterror.New("upload request failed outside the worker", clienterror.Incomplete))
		rec.clientFailure = true
	}
	return cloneOperation(rec.op), true
}

// FailExternal preserves cancellation and newer upload attempts when an older
// HTTP request finishes with an error. Staging is owned by the upload manager.
func (m *Manager) FailExternal(id string, observed time.Time, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec := m.records[id]
	// Status heartbeats and Pause/Resume may advance UpdatedAt without any new
	// upload data. Only a newer upload result/attempt invalidates this error.
	if rec == nil || !rec.external || rec.op.State != StateRunning || rec.externalProgressAt.After(observed) {
		return
	}
	m.failExternalLocked(rec, err)
}

func (m *Manager) failExternalLocked(rec *record, err error) {
	rec.op.Paused = false
	now := time.Now().UTC()
	rec.op.State, rec.op.Phase = StateFailed, "failed"
	slog.Error("external task failed", "id", rec.op.ID, "kind", rec.op.Kind, "err", err)
	rec.op.Error, rec.op.Message = clienterror.Text(err), "Upload stopped"
	rec.op.UpdatedAt, rec.op.FinishedAt = now, timePointer(now)
	rec.op.Cancellable, rec.cancel = false, nil
	resetTransferRate(rec)
	m.persistLocked(rec, true)
	m.broadcastLocked(rec.op)
	m.trimLocked()
}

func (m *Manager) Cancel(id string) bool {
	m.mu.Lock()
	rec := m.records[id]
	if rec == nil || !rec.op.Cancellable || !activeState(rec.op.State) {
		m.mu.Unlock()
		return false
	}
	rec.op.Paused = false
	if !rec.external && (rec.op.State == StateQueued || rec.op.State == StateAwaitingInput && rec.cancel == nil) {
		rec.op.Input = nil
		now := time.Now().UTC()
		rec.op.State = StateCancelled
		rec.op.Phase = "cancelled"
		rec.op.Message = "Cancelled before starting"
		rec.op.Cancellable = false
		rec.op.UpdatedAt = now
		rec.op.FinishedAt = timePointer(now)
		rec.op.QueuePosition = 0
		m.removeFromQueueLocked(id)
		m.persistLocked(rec, true)
		m.broadcastLocked(rec.op)
		m.refreshQueuePositionsLocked()
		m.mu.Unlock()
		return true
	}
	cancel := rec.cancel
	external := rec.external
	rec.op.State = StateCancelling
	rec.op.Phase = "cancelling"
	rec.op.Message = "Cancelling"
	rec.op.Cancellable = false
	resetTransferRate(rec)
	rec.op.UpdatedAt = time.Now().UTC()
	m.persistLocked(rec, true)
	m.broadcastLocked(rec.op)
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if external {
		m.finishExternalCancellation(id)
	}
	return true
}

func (m *Manager) finishExternalCancellation(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec := m.records[id]
	if rec == nil || rec.op.State != StateCancelling {
		return
	}
	now := time.Now().UTC()
	rec.op.State = StateCancelled
	rec.op.Phase = "cancelled"
	rec.op.Message = "Cancelled"
	resetTransferRate(rec)
	rec.op.UpdatedAt = now
	rec.op.FinishedAt = timePointer(now)
	rec.cancel = nil
	m.persistLocked(rec, true)
	m.broadcastLocked(rec.op)
}

func (m *Manager) SetExternalCancel(id string, cancel context.CancelFunc) (Operation, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec := m.records[id]
	if rec == nil || !rec.external || terminalState(rec.op.State) {
		return Operation{}, false
	}
	rec.cancel = cancel
	rec.op.Cancellable = true
	m.persistLocked(rec, false)
	m.broadcastLocked(rec.op)
	return cloneOperation(rec.op), true
}

func (m *Manager) List() []Operation {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Operation, 0, len(m.records))
	for _, rec := range m.records {
		out = append(out, cloneOperation(rec.op))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out
}

func (m *Manager) Get(id string) (Operation, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec := m.records[id]
	if rec == nil {
		return Operation{}, false
	}
	return cloneOperation(rec.op), true
}

func (m *Manager) SubmitInput(id string, data json.RawMessage) (Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec := m.records[id]
	if m.closed || rec == nil || rec.op.State != StateAwaitingInput || rec.cancel != nil || rec.inputHandler == nil {
		return Operation{}, errors.New("task is not waiting for a decision")
	}
	if err := rec.inputHandler(data); err != nil {
		return Operation{}, err
	}
	rec.op.Input = nil
	rec.op.State = StateQueued
	rec.op.Phase = "queued"
	rec.op.Message = "Decision accepted; queued to continue"
	rec.op.UpdatedAt = time.Now().UTC()
	m.queue = append(m.queue, id)
	m.persistLocked(rec, true)
	m.broadcastLocked(rec.op)
	m.refreshQueuePositionsLocked()
	m.scheduleLocked()
	return cloneOperation(rec.op), nil
}

func (m *Manager) MarkArtifactDownloaded(id string) (Operation, bool) {
	return m.markArchiveArtifact(id, "downloaded", "Downloaded · temporary archive removed")
}

func (m *Manager) MarkArtifactUnavailable(id string) (Operation, bool) {
	return m.markArchiveArtifact(id, "unavailable", "Temporary archive is no longer available")
}

func (m *Manager) markArchiveArtifact(id, phase, message string) (Operation, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec := m.records[id]
	if rec == nil || rec.op.Kind != "archive" || rec.op.State != StateCompleted {
		return Operation{}, false
	}
	if rec.op.Phase == phase && rec.op.Message == message {
		return cloneOperation(rec.op), true
	}
	rec.op.Phase = phase
	rec.op.Message = message
	rec.op.UpdatedAt = time.Now().UTC()
	m.persistLocked(rec, true)
	rec.lastBroadcast = time.Now()
	m.broadcastLocked(rec.op)
	return cloneOperation(rec.op), true
}

func (m *Manager) MoveQueued(id string, direction int) (Operation, bool) {
	if direction != -1 && direction != 1 {
		return Operation{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	rec := m.records[id]
	if rec == nil || rec.op.State != StateQueued {
		return Operation{}, false
	}
	index := -1
	for current, queuedID := range m.queue {
		if queuedID == id {
			index = current
			break
		}
	}
	target := index + direction
	if index < 0 || target < 0 || target >= len(m.queue) {
		return cloneOperation(rec.op), false
	}
	m.queue[index], m.queue[target] = m.queue[target], m.queue[index]
	m.refreshQueuePositionsLocked()
	return cloneOperation(rec.op), true
}

func (m *Manager) HasQueuedKind(kind string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, id := range m.queue {
		if rec := m.records[id]; rec != nil && rec.op.State == StateQueued && rec.op.Kind == kind {
			return true
		}
	}
	return false
}

func (m *Manager) removeFromQueueLocked(id string) {
	for index, queuedID := range m.queue {
		if queuedID == id {
			m.queue = append(m.queue[:index], m.queue[index+1:]...)
			return
		}
	}
}

func (m *Manager) ClearHistory() (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.historyCleanup != nil {
		for _, rec := range m.records {
			if terminalState(rec.op.State) {
				if err := m.historyCleanup(cloneOperation(rec.op)); err != nil {
					return 0, err
				}
			}
		}
	}

	if m.store != nil {
		if _, err := m.store.ClearHistory(); err != nil {
			return 0, err
		}
	}
	removed := 0
	for id, rec := range m.records {
		if terminalState(rec.op.State) {
			delete(m.records, id)
			removed++
		}
	}
	return removed, nil
}

func (m *Manager) RemoveHistory(id string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rec := m.records[id]
	if rec == nil || !terminalState(rec.op.State) {
		return false, nil
	}
	if m.historyCleanup != nil {
		if err := m.historyCleanup(cloneOperation(rec.op)); err != nil {
			return false, err
		}
	}
	if m.store != nil {
		removed, err := m.store.RemoveHistory(id)
		if err != nil || !removed {
			return false, err
		}
	}
	delete(m.records, id)
	return true, nil
}

func (m *Manager) Subscribe() (<-chan Operation, func()) {
	ch := make(chan Operation, 32)
	m.mu.Lock()
	m.subs[ch] = struct{}{}
	m.mu.Unlock()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			m.mu.Lock()
			delete(m.subs, ch)
			close(ch)
			m.mu.Unlock()
		})
	}
	return ch, cancel
}

func (m *Manager) Close(ctx context.Context) error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	now := time.Now().UTC()
	for _, rec := range m.records {
		if rec.op.State == StateQueued || rec.op.State == StateAwaitingInput && rec.cancel == nil {
			rec.op.Paused = false
			rec.op.Input = nil
			rec.op.State = StateInterrupted

			rec.op.Phase = "interrupted"
			rec.op.Message = "Interrupted by worker shutdown"
			rec.op.Cancellable = false
			rec.op.UpdatedAt = now
			rec.op.FinishedAt = timePointer(now)
			m.persistLocked(rec, true)
			m.broadcastLocked(rec.op)
		} else if rec.cancel != nil && activeState(rec.op.State) {
			rec.cancel()
		}
	}
	m.mu.Unlock()

	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		return ctx.Err()
	}
	if m.store != nil {
		return m.store.Close()
	}
	return nil
}

func (m *Manager) updateProgress(id string, p ProgressUpdate) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec := m.records[id]
	if rec == nil || terminalState(rec.op.State) || rec.op.State == StateCancelling || rec.op.State == StateCleaningUp {
		return
	}
	now := time.Now()
	stateChanged := rec.op.State != StateRunning
	phaseChanged := p.Phase != "" && rec.op.Phase != p.Phase
	unitChanged := rec.op.ProgressUnit != p.ProgressUnit
	rec.op.State = StateRunning
	rec.op.ProgressDone = maxInt64(0, p.ProgressDone)
	rec.op.ProgressTotal = maxInt64(0, p.ProgressTotal)
	rec.op.ProgressUnit = p.ProgressUnit
	rec.op.BytesDone = maxInt64(0, p.BytesDone)
	rec.op.BytesTotal = maxInt64(0, p.BytesTotal)
	rec.op.ItemsDone = maxInt64(0, p.ItemsDone)
	rec.op.ItemsTotal = maxInt64(0, p.ItemsTotal)
	if p.Phase != "" {
		rec.op.Phase = p.Phase
	}
	rec.op.Message = p.Message
	rec.op.UpdatedAt = now.UTC()
	m.updateTaskMetricsLocked(rec, now)

	// Per-file copy/flush/publish transitions are telemetry, not durable task
	// state changes. Persist and broadcast them at the normal progress cadence.
	// Conflict, cancellation and completion use their immediate state paths.
	transferProgress := (rec.op.Kind == "copy" || rec.op.Kind == "move") && transferIOPhase(p.Phase)
	changed := stateChanged || unitChanged || phaseChanged && !transferProgress
	terminalProgress := !transferProgress && p.ProgressTotal > 0 && p.ProgressDone >= p.ProgressTotal
	if changed || terminalProgress || time.Since(rec.lastPersistence) >= time.Second {
		m.persistLocked(rec, changed)
	}
	if changed || terminalProgress || time.Since(rec.lastBroadcast) >= 100*time.Millisecond {
		rec.lastBroadcast = time.Now()
		m.broadcastLocked(rec.op)
	}
}

func transferIOPhase(phase string) bool {
	switch phase {
	case "copying", "flushing", "publishing", "removing_source":
		return true
	}
	return false
}

func (m *Manager) setStatus(id string, state State, phase, message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec := m.records[id]
	if rec == nil || terminalState(rec.op.State) || rec.op.State == StateCancelling || rec.op.State == StateCleaningUp {
		return
	}
	changed := rec.op.State != state || rec.op.Phase != phase || rec.op.Message != message
	if !changed {
		return
	}
	phaseChanged := rec.op.Phase != phase
	rec.op.State = state
	rec.op.Phase = phase
	rec.op.Message = message
	if phaseChanged {
		rec.op.ProgressDone, rec.op.ProgressTotal, rec.op.ProgressUnit = 0, 0, ""
		rec.op.BytesDone, rec.op.BytesTotal = 0, 0
		rec.op.ItemsDone, rec.op.ItemsTotal = 0, 0
		resetTransferRate(rec)
	}
	rec.op.UpdatedAt = time.Now().UTC()
	m.persistLocked(rec, true)
	rec.lastBroadcast = time.Now()
	m.broadcastLocked(rec.op)
}

func (m *Manager) persistLocked(rec *record, stateChanged bool) {
	if m.store == nil {
		return
	}
	if err := m.store.Upsert(rec.op, stateChanged); err != nil {
		slog.Error("persist task", slog.String("id", rec.op.ID), slog.String("err", err.Error()))
		return
	}
	rec.lastPersistence = time.Now()
}

func (m *Manager) refreshQueuePositionsLocked() {
	for index, id := range m.queue {
		rec := m.records[id]
		if rec == nil || rec.op.State != StateQueued {
			continue
		}
		position := index + 1
		if rec.op.QueuePosition == position {
			continue
		}
		rec.op.QueuePosition = position
		rec.op.UpdatedAt = time.Now().UTC()
		m.persistLocked(rec, false)
		m.broadcastLocked(rec.op)
	}
}

func (m *Manager) broadcastLocked(op Operation) {
	snapshot := cloneOperation(op)
	for ch := range m.subs {
		select {
		case ch <- snapshot:
		default:
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- snapshot:
			default:
			}
		}
	}
}

func (m *Manager) updateTransferRate(id string) {
	ticker := time.NewTicker(transferRateHeartbeat)
	defer ticker.Stop()
	for range ticker.C {
		m.mu.Lock()
		now := time.Now()
		rec := m.records[id]
		if rec == nil || terminalState(rec.op.State) || len(rec.rateSamples) == 0 {
			if rec != nil {
				rec.rateHeartbeat = false
			}
			m.mu.Unlock()
			return
		}
		// Include time spent waiting for disk or the next upload chunk.
		m.updateTaskMetricsLocked(rec, now)
		rec.op.UpdatedAt = now.UTC()
		m.broadcastLocked(rec.op)
		m.mu.Unlock()
	}
}

func resetTransferRate(rec *record) {
	rec.op.BytesPerSecond = nil
	rec.op.ItemsPerSecond = nil
	rec.op.ETASeconds = nil
	rec.rateSamples = nil
	rec.rateUnit = ""
}

func (m *Manager) trimLocked() {
	if len(m.records) <= m.maxKeep {
		return
	}
	finished := make([]*record, 0)
	for _, rec := range m.records {
		if terminalState(rec.op.State) {
			finished = append(finished, rec)
		}
	}
	sort.Slice(finished, func(i, j int) bool { return finished[i].op.UpdatedAt.Before(finished[j].op.UpdatedAt) })
	remaining := len(finished)
	cleanupFailed := false
	for _, rec := range finished {
		if remaining <= m.maxKeep {
			break
		}
		if m.historyCleanup != nil {
			if err := m.historyCleanup(cloneOperation(rec.op)); err != nil {
				slog.Error("clean evicted task artifacts", slog.String("id", rec.op.ID), slog.String("err", err.Error()))
				cleanupFailed = true
				continue
			}
		}
		if m.store != nil {
			if _, err := m.store.RemoveHistory(rec.op.ID); err != nil {
				slog.Error("remove evicted task", slog.String("id", rec.op.ID), slog.String("err", err.Error()))
				cleanupFailed = true
				continue
			}
		}
		delete(m.records, rec.op.ID)
		remaining--
	}
	if m.store != nil && !cleanupFailed {
		if err := m.store.Trim(m.maxKeep); err != nil {
			slog.Error("trim task history", slog.String("err", err.Error()))
		}
	}
}

// SetHistoryCleanup registers artifact cleanup before history removal or eviction.
// The callback runs under the manager lock and must not call manager methods.
// A cleanup failure retains the record so a later trim can retry it.
func (m *Manager) SetHistoryCleanup(cleanup func(Operation) error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.historyCleanup = cleanup
}

func activeState(state State) bool {
	return state == StateQueued || state == StatePreparing || state == StateAwaitingInput || state == StateRunning || state == StateCancelling || state == StateCleaningUp
}

func terminalState(state State) bool {
	return state == StateCompleted || state == StateFailed || state == StateCancelled || state == StateInterrupted
}

func cloneOperation(op Operation) Operation {
	out := op
	out.UndoData = append(json.RawMessage(nil), op.UndoData...)
	out.Input = append(json.RawMessage(nil), op.Input...)
	out.Sources = append([]string(nil), op.Sources...)
	if op.BytesPerSecond != nil {
		rate := *op.BytesPerSecond
		out.BytesPerSecond = &rate
	}
	if op.ItemsPerSecond != nil {
		rate := *op.ItemsPerSecond
		out.ItemsPerSecond = &rate
	}
	if op.ETASeconds != nil {
		seconds := *op.ETASeconds
		out.ETASeconds = &seconds
	}
	if op.FinishedAt != nil {
		finished := *op.FinishedAt
		out.FinishedAt = &finished
	}
	return out
}

func newID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err == nil {
		return hex.EncodeToString(b[:])
	}
	return time.Now().UTC().Format("20060102T150405.000000000")
}

func timePointer(value time.Time) *time.Time {
	return &value
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}
