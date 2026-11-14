package httpserver

import (
	"context"
	"encoding/json"
	"net/http"

	"webfile/internal/filesystem"
	"webfile/internal/operations"
)

func (s *Server) invalidateUndo(w http.ResponseWriter) bool {
	if s.ops != nil {
		if err := s.ops.InvalidateUndo(); err != nil {
			writeError(w, err)
			return false
		}
	}
	return true
}

func (s *Server) undoOperation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	snapshot, ok := s.ops.Get(id)
	if !ok || !snapshot.UndoAvailable {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Undo is unavailable for this task."})
		return
	}
	var plan filesystem.UndoPlan
	if err := json.Unmarshal(snapshot.UndoData, &plan); err != nil || !s.ops.ValidUndoJournal(id, plan.Journal) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "The undo record could not be read."})
		return
	}
	reader, err := filesystem.OpenUndoJournal(plan)
	if err != nil {
		writeError(w, err)
		return
	}
	original, err := s.ops.ClaimUndo(id)
	if err != nil {
		reader.Close()
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Undo is unavailable for this task."})
		return
	}
	op := s.ops.StartTask("undo_"+original.Kind, original.Sources, original.Destination, operations.TaskOptions{
		OnFinish:      func() { _ = reader.Close() },
		Cancellable:   true,
		CleanupPolicy: "Items already undone are kept. Remaining items stay in place. Undo cannot be retried.",
	}, func(ctx context.Context, reporter operations.Reporter) error {
		reporter.UpdatePhase("scanning", 0, 0, 0, 0, "Checking files before undo")
		return s.files.UndoTransferJournal(ctx, plan, reader, func(p filesystem.TransferProgress) {
			reporter.UpdatePhase(p.Phase, p.BytesDone, p.BytesTotal, p.ItemsDone, p.ItemsTotal, p.Message)
		})
	})
	writeJSON(w, http.StatusAccepted, op)
}
