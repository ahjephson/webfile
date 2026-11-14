package operations

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const UndoLifetime = time.Hour

type undoCheckpoint struct {
	Data      json.RawMessage `json:"data,omitempty"`
	Reason    string          `json:"reason,omitempty"`
	ExpiresAt *time.Time      `json:"expires_at,omitempty"`
}

func (r Reporter) SetUndo(plan any, reason string) error {
	r.manager.mu.Lock()
	eligible := r.manager.undoOwner == r.id
	r.manager.mu.Unlock()
	if !eligible {
		return nil
	}
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
	if rec := r.manager.records[r.id]; rec != nil && r.manager.undoOwner == r.id {
		rec.op.UndoData, rec.op.UndoReason = data, reason
	}
	return nil
}

// Only small root/receipt-file metadata is stored in the task database.
func (r Reporter) UndoJournalPath() (string, error) {
	m := r.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.undoOwner != r.id {
		return "", nil
	}
	if m.undoDir == "" {
		dir, err := os.MkdirTemp("", "webfile-undo-")
		if err != nil {
			return "", err
		}
		m.undoDir, m.undoTemporary = dir, true
	}
	return filepath.Join(m.undoDir, r.id+".undo"), nil
}
func (r Reporter) KeepUndoJournal(discard func()) bool {
	m := r.manager
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed || m.undoOwner != r.id || m.records[r.id] == nil {
		discard()
		return false
	}
	m.records[r.id].undoCleanup = discard
	return true
}
func (m *Manager) ValidUndoJournal(id, name string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.undoDir != "" && name == filepath.Join(m.undoDir, id+".undo")
}
func (m *Manager) discardJournalLocked(rec *record) {
	if rec.undoCleanup != nil {
		rec.undoCleanup()
		rec.undoCleanup = nil
	}
	if m.undoDir != "" && journalFileName(rec.op.ID+".undo") {
		_ = os.Remove(filepath.Join(m.undoDir, rec.op.ID+".undo"))
	}
}
func (m *Manager) ClaimUndo(id string) (Operation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_ = m.expireUndoLocked(time.Now().UTC())
	rec := m.records[id]
	if m.closed || rec == nil || m.undoOwner != id || rec.op.State != StateCompleted || !rec.op.UndoAvailable || len(rec.op.UndoData) == 0 || rec.op.UndoExpiresAt == nil || !time.Now().Before(*rec.op.UndoExpiresAt) || (rec.op.Kind != "copy" && rec.op.Kind != "move") {
		return Operation{}, errors.New("undo is unavailable for this task")
	}
	original := cloneOperation(rec.op)
	if err := m.replaceUndoLocked("", "Undo has been requested. See the undo task for its result."); err != nil {
		return Operation{}, err
	}
	return original, nil
}
func (m *Manager) advanceUndoLocked(id string) error {
	return m.replaceUndoLocked(id, "A later filesystem operation replaced this undo set.")
}
func (m *Manager) replaceUndoLocked(id, reason string) error {
	if m.store != nil {
		if err := m.store.advanceUndo(id); err != nil {
			return err
		}
	}
	if m.undoTimer != nil {
		m.undoTimer.Stop()
		m.undoTimer = nil
	}
	m.undoOwner = id
	for _, rec := range m.records {
		hadUndo := len(rec.op.UndoData) > 0 || rec.op.UndoAvailable || rec.undoCleanup != nil
		m.discardJournalLocked(rec)
		if !hadUndo {
			continue
		}
		rec.op.UndoData, rec.op.UndoAvailable, rec.op.UndoExpiresAt = nil, false, nil
		rec.op.UndoReason = reason
		rec.op.UpdatedAt = time.Now().UTC()
		m.broadcastLocked(rec.op)
	}
	return nil
}
func (m *Manager) InvalidateUndo() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errors.New("tasks are unavailable")
	}
	return m.advanceUndoLocked("")
}
func (m *Manager) expireUndoLocked(now time.Time) error {
	rec := m.records[m.undoOwner]
	if rec == nil || !rec.op.UndoAvailable || rec.op.UndoExpiresAt == nil || now.Before(*rec.op.UndoExpiresAt) {
		return nil
	}
	return m.replaceUndoLocked("", "Undo expired one hour after completion.")
}
func (m *Manager) armUndoTimerLocked() {
	if m.undoTimer != nil {
		m.undoTimer.Stop()
		m.undoTimer = nil
	}
	rec := m.records[m.undoOwner]
	if m.closed || rec == nil || !rec.op.UndoAvailable || rec.op.UndoExpiresAt == nil {
		return
	}
	wait := time.Until(*rec.op.UndoExpiresAt)
	if wait < time.Second {
		wait = time.Second
	}
	m.undoTimer = time.AfterFunc(wait, func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.closed {
			return
		}
		if err := m.expireUndoLocked(time.Now().UTC()); err != nil {
			slog.Error("expire undo", "err", err)
		}
		m.armUndoTimerLocked()
	})
}
func (m *Manager) recoverUndo() error {
	rec := m.records[m.undoOwner]
	if rec == nil || rec.op.State != StateCompleted || rec.op.UndoExpiresAt == nil || !time.Now().Before(*rec.op.UndoExpiresAt) || len(rec.op.UndoData) == 0 {
		if err := m.replaceUndoLocked("", "Undo is unavailable after an interrupted or expired operation."); err != nil {
			return err
		}
	} else {
		if _, err := os.Stat(filepath.Join(m.undoDir, rec.op.ID+".undo")); err != nil {
			if err := m.replaceUndoLocked("", "The undo journal is no longer available."); err != nil {
				return err
			}
		}
	}
	entries, err := os.ReadDir(m.undoDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() != m.undoOwner+".undo" && !entry.IsDir() && journalFileName(entry.Name()) {
			if err := os.Remove(filepath.Join(m.undoDir, entry.Name())); err != nil {
				return err
			}
		}
	}
	m.armUndoTimerLocked()
	return nil
}

func journalFileName(name string) bool {
	if !strings.HasSuffix(name, ".undo") {
		return false
	}
	id := strings.TrimSuffix(name, ".undo")
	if bytes, err := hex.DecodeString(id); err == nil && len(bytes) == 12 {
		return true
	}
	_, err := time.Parse("20060102T150405.000000000", id)
	return err == nil
}
func changesUserFiles(kind string) bool {
	switch kind {
	case "copy", "move", "delete", "extract", "upload", "undo_copy", "undo_move":
		return true
	}
	return false
}
