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

func TestQueuedUploadsStartIndividuallyAndCancelledUploadsCannotRestart(t *testing.T) {
	target, root := testDownloadServer(t)
	target.uploads = filesystem.NewUploadManager(target.files.Resolver(), t.TempDir())
	target.ops = operations.New(50)
	defer target.ops.Close(context.Background())
	attempt := strings.Repeat("a", 32)
	type initialUpload struct {
		Upload    filesystem.UploadSession `json:"upload"`
		Operation operations.Operation     `json:"operation"`
	}
	init := func(name string) initialUpload {
		t.Helper()
		response := httptest.NewRecorder()
		target.initUpload(response, httptest.NewRequest(http.MethodPost, "/api/fs/uploads/init", strings.NewReader(`{"dir":"/Files","name":"`+name+`","size":1,"queued":true,"attempt_id":"`+attempt+`"}`)))
		if response.Code != http.StatusOK {
			t.Fatalf("init: HTTP %d %s", response.Code, response.Body.String())
		}
		var result initialUpload
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Operation.State != operations.StateQueued || !result.Operation.Cancellable {
			t.Fatalf("not queued: %#v", result.Operation)
		}
		return result
	}
	first, second := init("first.txt"), init("second.txt")
	chunk := func(upload initialUpload, suppliedAttempt string) int {
		response := httptest.NewRecorder()
		target.uploadChunk(response, httptest.NewRequest(http.MethodPut, "/api/fs/uploads/chunk?id="+upload.Upload.ID+"&offset=0&attempt_id="+suppliedAttempt, strings.NewReader("x")))
		return response.Code
	}
	if code := chunk(first, strings.Repeat("b", 32)); code != http.StatusConflict {
		t.Fatalf("stale attempt: HTTP %d", code)
	}
	if code := chunk(first, attempt); code != http.StatusOK {
		t.Fatalf("first upload: HTTP %d", code)
	}
	if data, err := os.ReadFile(filepath.Join(root, "first.txt")); err != nil || string(data) != "x" {
		t.Fatalf("publication: %q %v", data, err)
	}
	if op, _ := target.ops.Get(second.Operation.ID); op.State != operations.StateQueued {
		t.Fatalf("second started early: %#v", op)
	}
	if !target.ops.Cancel(second.Operation.ID) {
		t.Fatal("queued cancellation failed")
	}
	if code := chunk(second, attempt); code != http.StatusConflict {
		t.Fatalf("cancelled upload restarted: HTTP %d", code)
	}
	if _, err := os.Stat(filepath.Join(root, "second.txt")); !os.IsNotExist(err) {
		t.Fatalf("cancelled file published: %v", err)
	}
	// Cancellation must remove the staging record, not merely hide the task.
	if err := target.uploads.Cancel(second.Upload.ID); !os.IsNotExist(err) {
		t.Fatalf("cancelled staging remains: %v", err)
	}
}

func TestQueuedUploadRejectsDuplicateAndRecoversAbandonedAttempt(t *testing.T) {
	target, _ := testDownloadServer(t)
	target.uploads = filesystem.NewUploadManager(target.files.Resolver(), t.TempDir())
	target.ops = operations.New(50)
	defer target.ops.Close(context.Background())
	init := func(attempt string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		target.initUpload(response, httptest.NewRequest(http.MethodPost, "/api/fs/uploads/init", strings.NewReader(`{"dir":"/Files","name":"retry.txt","size":1,"queued":true,"attempt_id":"`+attempt+`"}`)))
		return response
	}
	attempt := strings.Repeat("a", 32)
	response := init(attempt)
	var first struct {
		Operation operations.Operation `json:"operation"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if response = init(strings.Repeat("b", 32)); response.Code != http.StatusPreconditionFailed {
		t.Fatalf("duplicate replaced queued attempt: HTTP %d", response.Code)
	}
	failed, _ := target.ops.ReportExternalFailure(first.Operation.ID, attempt, nil)
	if failed.State != operations.StateFailed {
		t.Fatalf("abandoned queue remains active: %#v", failed)
	}
	if response = init(strings.Repeat("b", 32)); response.Code != http.StatusOK {
		t.Fatalf("retry: HTTP %d %s", response.Code, response.Body.String())
	}
}
