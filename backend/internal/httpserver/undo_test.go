package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"webfile/internal/operations"
)

func TestUndoOperationIsSingleUseAndDoesNotExposeJournal(t *testing.T) {
	s, root := testDownloadServer(t)
	t.Cleanup(s.files.Close)
	s.ops = operations.New(50)
	t.Cleanup(func() { s.ops.Close(context.Background()) })
	os.Mkdir(filepath.Join(root, "destination"), 0700)
	os.WriteFile(filepath.Join(root, "original"), []byte("content"), 0600)
	response := httptest.NewRecorder()
	s.startOperation(response, httptest.NewRequest(http.MethodPost, "/api/fs/operations", bytes.NewBufferString(`{"kind":"copy","sources":["/Files/original"],"destination":"/Files/destination"}`)))
	if response.Code != http.StatusAccepted {
		t.Fatal(response.Code, response.Body.String())
	}
	var original operations.Operation
	if err := json.Unmarshal(response.Body.Bytes(), &original); err != nil {
		t.Fatal(err)
	}
	wait := func(id string) operations.Operation {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			op, _ := s.ops.Get(id)
			if op.State == operations.StateCompleted {
				return op
			}
			if op.State == operations.StateFailed {
				t.Fatal(op.Error)
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatal("task did not finish")
		return operations.Operation{}
	}
	if !wait(original.ID).UndoAvailable {
		t.Fatal("undo not advertised")
	}
	undo := func() *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/api/fs/operations/"+original.ID+"/undo", nil)
		r.SetPathValue("id", original.ID)
		w := httptest.NewRecorder()
		s.undoOperation(w, r)
		return w
	}
	w := undo()
	if w.Code != http.StatusAccepted {
		t.Fatal(w.Code, w.Body.String())
	}
	var reversed operations.Operation
	if err := json.Unmarshal(w.Body.Bytes(), &reversed); err != nil {
		t.Fatal(err)
	}
	if reversed.Kind != "undo_copy" {
		t.Fatal(reversed.Kind)
	}
	wait(reversed.ID)
	if undo().Code != http.StatusConflict {
		t.Fatal("duplicate undo accepted")
	}
	if _, err := os.Stat(filepath.Join(root, "destination", "original")); !os.IsNotExist(err) {
		t.Fatal("copy remains", err)
	}
	if _, err := os.Stat(filepath.Join(root, "original")); err != nil {
		t.Fatal("source removed", err)
	}
}
