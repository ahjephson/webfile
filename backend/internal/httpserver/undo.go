package httpserver

import (
	"context"
	"encoding/json"
	"net/http"

	"webfile/internal/filesystem"
	"webfile/internal/operations"
)

func (s *Server) undoOperation(w http.ResponseWriter, r *http.Request) {
	original, err := s.ops.ClaimUndo(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "Undo is unavailable for this task."})
		return
	}
	var plan filesystem.UndoPlan
	if err := json.Unmarshal(original.UndoData, &plan); err != nil {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "The undo record could not be read."})
		return
	}
	op := s.ops.StartTask("undo_"+original.Kind, original.Sources, original.Destination, operations.TaskOptions{
		Cancellable:   true,
		CleanupPolicy: "Items already undone are kept. Remaining items stay in place. Undo cannot be retried.",
	}, func(ctx context.Context, reporter operations.Reporter) error {
		reporter.UpdatePhase("scanning", 0, 0, 0, 0, "Checking files before undo")
		return s.files.UndoTransfer(ctx, plan, func(p filesystem.TransferProgress) {
			reporter.UpdatePhase(p.Phase, p.BytesDone, p.BytesTotal, p.ItemsDone, p.ItemsTotal, p.Message)
		})
	})
	writeJSON(w, http.StatusAccepted, op)
}
