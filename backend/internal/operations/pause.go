package operations

import (
	"errors"
	"time"

	"webfile/internal/taskcontrol"
)

func (m *Manager) SetPaused(id string, paused bool) (Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec := m.records[id]
	if m.closed || rec == nil || !rec.op.Cancellable ||
		(rec.op.State != StateQueued && rec.op.State != StatePreparing && rec.op.State != StateRunning && rec.op.State != StateAwaitingInput) {
		return Operation{}, errors.New("task cannot be paused or resumed")
	}
	switch rec.op.Kind {
	case "copy", "move", "delete", "extract", "archive", "upload":
	default:
		return Operation{}, errors.New("task does not support pause")
	}
	if rec.op.Paused == paused {
		return cloneOperation(rec.op), nil
	}
	if rec.control == nil {
		rec.control = taskcontrol.New()
	}
	if err := rec.control.SetPaused(paused); err != nil {
		return Operation{}, err
	}
	rec.op.Paused = paused
	rec.op.UpdatedAt = time.Now().UTC()
	resetTransferRate(rec)
	if !paused {
		m.updateTaskMetricsLocked(rec, rec.op.UpdatedAt)
	}
	m.persistLocked(rec, true)
	m.broadcastLocked(rec.op)
	if !paused {
		m.scheduleLocked()
	}
	return cloneOperation(rec.op), nil
}
