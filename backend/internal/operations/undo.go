package operations

import (
	"encoding/json"
	"errors"
	"time"
)

type undoCheckpoint struct {
	Data   json.RawMessage `json:"data,omitempty"`
	Reason string          `json:"reason,omitempty"`
}

func (r Reporter) SetUndo(plan any, reason string) error {
	var data json.RawMessage
	if plan != nil {
		var err error
		data, err = json.Marshal(plan)
		if err != nil {
			return err
		}
	}
	r.manager.mu.Lock()
	defer r.manager.mu.Unlock()
	if rec := r.manager.records[r.id]; rec != nil {
		rec.op.UndoData = data
		rec.op.UndoReason = reason
	}
	return nil
}

// ClaimUndo is single-use across concurrent requests and restarts. Persist the
// claim before returning any filesystem instructions to the worker.
func (m *Manager) ClaimUndo(id string) (Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec := m.records[id]
	if m.closed || rec == nil || rec.op.State != StateCompleted || !rec.op.UndoAvailable || len(rec.op.UndoData) == 0 || (rec.op.Kind != "copy" && rec.op.Kind != "move") {
		return Operation{}, errors.New("undo is unavailable for this task")
	}
	original := cloneOperation(rec.op)
	claimed := cloneOperation(rec.op)
	claimed.UndoData = nil
	claimed.UndoAvailable = false
	claimed.UndoReason = "Undo has been requested. See the undo task for its result."
	claimed.UpdatedAt = time.Now().UTC()
	if m.store != nil {
		if err := m.store.Upsert(claimed, true); err != nil {
			return Operation{}, err
		}
	}
	rec.op = claimed
	m.broadcastLocked(claimed)
	return original, nil
}
