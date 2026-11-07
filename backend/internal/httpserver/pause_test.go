package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"webfile/internal/filesystem"
	"webfile/internal/operations"
)

func TestPauseAndResumeUploadAtChunkBoundaries(t *testing.T) {
	s, root := testDownloadServer(t)
	s.ops = operations.New(20)
	defer s.ops.Close(context.Background())
	s.uploads = filesystem.NewUploadManager(s.files.Resolver(), t.TempDir())
	attempt := strings.Repeat("a", 32)
	response := httptest.NewRecorder()
	s.initUpload(response, httptest.NewRequest(http.MethodPost, "/api/fs/uploads/init", strings.NewReader(`{"dir":"/Files","name":"paused.txt","size":2,"queued":true,"attempt_id":"`+attempt+`"}`)))
	var initial struct {
		Upload    filesystem.UploadSession `json:"upload"`
		Operation operations.Operation     `json:"operation"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &initial); err != nil {
		t.Fatal(err)
	}
	control := func(paused bool, want int) {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "/api/fs/operations/control", nil)
		r.SetPathValue("id", initial.Operation.ID)
		w := httptest.NewRecorder()
		if paused {
			s.pauseOperation(w, r)
		} else {
			s.resumeOperation(w, r)
		}
		if w.Code != want {
			t.Fatalf("control: HTTP %d %s", w.Code, w.Body.String())
		}
		if want == http.StatusOK {
			var op operations.Operation
			if err := json.Unmarshal(w.Body.Bytes(), &op); err != nil || op.Paused != paused {
				t.Fatalf("control state=%+v error=%v", op, err)
			}
		}
	}
	chunk := func(offset string, text string, want int) {
		t.Helper()
		w := httptest.NewRecorder()
		s.uploadChunk(w, httptest.NewRequest(http.MethodPut, "/api/fs/uploads/chunk?id="+initial.Upload.ID+"&offset="+offset+"&attempt_id="+attempt, strings.NewReader(text)))
		if w.Code != want {
			t.Fatalf("chunk: HTTP %d %s", w.Code, w.Body.String())
		}
		if want == http.StatusConflict && !strings.Contains(w.Body.String(), "operation_paused") {
			t.Fatal(w.Body.String())
		}
	}
	control(true, http.StatusOK)
	chunk("0", "a", http.StatusConflict)
	control(false, http.StatusOK)
	chunk("0", "a", http.StatusOK)
	control(true, http.StatusOK)
	chunk("1", "b", http.StatusConflict)
	control(false, http.StatusOK)
	chunk("1", "b", http.StatusOK)
	data, err := os.ReadFile(filepath.Join(root, "paused.txt"))
	if err != nil || string(data) != "ab" {
		t.Fatalf("resumed file: %q %v", data, err)
	}
	control(true, http.StatusConflict)
}
