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
	Input          json.RawMessage `json:"input,omitempty"`
	ID             string          `json:"id"`
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
	inputHandler     func(json.RawMessage) error
	op               Operation
	run              RunFunc
	cleanup          CleanupFunc
	cancel           context.CancelFunc
	external         bool
	lastBroadcast    time.Time
	lastPersistence  time.Time
	rateSamples      []transferRateSample
	lastByteProgress time.Time
	rateHeartbeat    bool
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
		if activeState(operation.State) {
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
		op.Error = "task manager is closed"
		op.Cancellable = false
		op.FinishedAt = timePointer(now)
		m.mu.Unlock()
		return op
	}
	rec := &record{op: op, run: run, cleanup: options.Cleanup, inputHandler: options.InputHandler}
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
			if rec != nil && rec.op.State == StateQueued && rec.run != nil && m.runningKinds[rec.op.Kind] == 0 {
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
	err := run(ctx, Reporter{manager: m, id: id})

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
		cleanupErr := cleanup(cleanupCtx, snapshot)
		cancel()

		m.mu.Lock()
		rec = m.records[id]
		if rec != nil && cleanupErr != nil {
			rec.op.CleanupError = cleanupErr.Error()
			err = cleanupErr
			cancelled = false
		}
	}
	if rec != nil {
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
			rec.op.Error = err.Error()
		default:
			rec.op.State = StateCompleted
			rec.op.Phase = "completed"
			if rec.op.Message == "" || rec.op.Message == "Starting" {
				rec.op.Message = "Completed"
			}
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

func (m *Manager) UpsertExternal(id, kind string, sources []string, destination string, state State, bytesDone, bytesTotal, itemsDone, itemsTotal int64, message, errText string) Operation {
	now := time.Now().UTC()
	m.mu.Lock()
	defer m.mu.Unlock()
	rec := m.records[id]
	previousState := State("")
	if rec == nil {
		rec = &record{op: Operation{ID: id, Kind: kind, Sources: append([]string(nil), sources...), Destination: destination, StartedAt: now}, external: true}
		m.records[id] = rec
	} else {
		previousState = rec.op.State
	}
	rec.external = true
	rec.op.Kind = kind
	rec.op.Sources = append([]string(nil), sources...)
	rec.op.Destination = destination
	rec.op.State = state
	rec.op.Phase = string(state)
	rec.op.BytesDone = bytesDone
	rec.op.BytesTotal = bytesTotal
	resetTransferRate(rec)
	rec.op.ProgressDone, rec.op.ProgressTotal, rec.op.ProgressUnit = primaryProgress(bytesDone, bytesTotal, itemsDone, itemsTotal)
	rec.op.ItemsDone = itemsDone
	rec.op.ItemsTotal = itemsTotal
	rec.op.Message = message
	rec.op.Error = errText
	rec.op.UpdatedAt = now
	rec.op.Cancellable = rec.cancel != nil && activeState(state)
	if terminalState(state) {
		rec.op.FinishedAt = timePointer(now)
		rec.op.Cancellable = false
	} else {
		rec.op.FinishedAt = nil
	}
	m.persistLocked(rec, previousState != state)
	m.trimLocked()
	m.broadcastLocked(rec.op)
	return cloneOperation(rec.op)
}

func (m *Manager) Cancel(id string) bool {
	m.mu.Lock()
	rec := m.records[id]
	if rec == nil || !rec.op.Cancellable || !activeState(rec.op.State) {
		m.mu.Unlock()
		return false
	}
	if rec.op.State == StateQueued || rec.op.State == StateAwaitingInput && rec.cancel == nil && !rec.external {
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
	previousBytes := rec.op.BytesDone
	nextPhase := rec.op.Phase
	if p.Phase != "" {
		nextPhase = p.Phase
	}
	if phaseChanged || p.BytesDone < previousBytes || nextPhase != transferRateActivePhase || p.BytesTotal <= 0 {
		resetTransferRate(rec)
	}
	if nextPhase == transferRateActivePhase && p.BytesTotal > 0 && p.BytesDone > previousBytes {
		rec.lastByteProgress = now
		rec.rateSamples, rec.op.BytesPerSecond = appendTransferRateSample(rec.rateSamples, now, p.BytesDone)
	}
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
	if rec.op.BytesPerSecond != nil && !rec.rateHeartbeat {
		rec.rateHeartbeat = true
		go m.expireTransferRate(id)
	}

	changed := stateChanged || phaseChanged || unitChanged
	terminalProgress := p.ProgressTotal > 0 && p.ProgressDone >= p.ProgressTotal
	if changed || terminalProgress || time.Since(rec.lastPersistence) >= time.Second {
		m.persistLocked(rec, changed)
	}
	if changed || terminalProgress || time.Since(rec.lastBroadcast) >= 100*time.Millisecond {
		rec.lastBroadcast = time.Now()
		m.broadcastLocked(rec.op)
	}
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

func (m *Manager) expireTransferRate(id string) {
	ticker := time.NewTicker(transferRateHeartbeat)
	defer ticker.Stop()
	for now := range ticker.C {
		m.mu.Lock()
		rec := m.records[id]
		if rec == nil || terminalState(rec.op.State) || rec.op.BytesPerSecond == nil {
			if rec != nil {
				rec.rateHeartbeat = false
			}
			m.mu.Unlock()
			return
		}
		if !transferRateExpired(rec.lastByteProgress, now) {
			m.mu.Unlock()
			continue
		}
		rec.op.BytesPerSecond = nil
		rec.rateSamples = nil
		rec.rateHeartbeat = false
		rec.op.UpdatedAt = now.UTC()
		m.broadcastLocked(rec.op)
		m.mu.Unlock()
		return
	}
}

func resetTransferRate(rec *record) {
	rec.op.BytesPerSecond = nil
	rec.rateSamples = nil
	rec.lastByteProgress = time.Time{}
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

// SetHistoryCleanup registers artifact cleanup before automatic history eviction.
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
	out.Input = append(json.RawMessage(nil), op.Input...)
	out.Sources = append([]string(nil), op.Sources...)
	if op.BytesPerSecond != nil {
		rate := *op.BytesPerSecond
		out.BytesPerSecond = &rate
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
