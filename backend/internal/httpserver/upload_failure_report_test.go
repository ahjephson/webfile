package httpserver

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"webfile/internal/clienterror"
	"webfile/internal/filesystem"
	"webfile/internal/operations"
)

type initializedUpload struct {
	Upload    filesystem.UploadSession `json:"upload"`
	Operation operations.Operation     `json:"operation"`
}

func initializeReportedUpload(t *testing.T, server *Server) initializedUpload {
	t.Helper()
	response := httptest.NewRecorder()
	server.initUpload(response, httptest.NewRequest(http.MethodPost, "/api/fs/uploads/init", strings.NewReader(`{"dir":"/Files","name":"proxy-failure.bin","size":4,"last_modified":1}`)))
	var result initializedUpload
	if response.Code != 200 {
		t.Fatalf("init: %d %s", response.Code, response.Body.String())
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func reportFailedUpload(t *testing.T, server *Server, operation operations.Operation) operations.Operation {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"id": operation.ID, "attempt_id": operation.AttemptID})
	response := httptest.NewRecorder()
	server.reportUploadFailure(response, httptest.NewRequest(http.MethodPost, "/api/fs/uploads/failure", strings.NewReader(string(body))))
	if response.Code != 200 {
		t.Fatalf("report: %d %s", response.Code, response.Body.String())
	}
	var result operations.Operation
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestProxyFailureMarksUploadFailedWithoutDiscardingResumeData(t *testing.T) {
	server, _ := testDownloadServer(t)
	server.uploads = filesystem.NewUploadManager(server.files.Resolver(), t.TempDir())
	server.ops = operations.New(50)
	defer server.ops.Close(context.Background())
	initial := initializeReportedUpload(t, server)
	failed := reportFailedUpload(t, server, initial.Operation)
	if failed.State != operations.StateFailed || failed.Error != string(clienterror.Incomplete) || failed.Cancellable || failed.FinishedAt == nil {
		t.Fatalf("not failed: %#v", failed)
	}
	resumed := initializeReportedUpload(t, server)
	if resumed.Upload.ID != initial.Upload.ID || resumed.Operation.AttemptID == initial.Operation.AttemptID {
		t.Fatal("retry must reuse staging with a new attempt ID")
	}
	stale := reportFailedUpload(t, server, initial.Operation)
	if stale.State != operations.StateRunning || stale.AttemptID != resumed.Operation.AttemptID {
		t.Fatalf("stale report overwrote retry: %#v", stale)
	}
	response := httptest.NewRecorder()
	server.uploadChunk(response, httptest.NewRequest(http.MethodPut, "/api/fs/uploads/chunk?id="+resumed.Upload.ID+"&offset=0&attempt_id="+resumed.Operation.AttemptID, strings.NewReader("data")))
	if response.Code != 200 {
		t.Fatalf("retry: %d %s", response.Code, response.Body.String())
	}
	if completed := reportFailedUpload(t, server, resumed.Operation); completed.State != operations.StateCompleted {
		t.Fatalf("late report overwrote completed upload: %#v", completed)
	}
}

func TestReportedFailureInterruptsWriterAndRetainsPartialUpload(t *testing.T) {
	server, _ := testDownloadServer(t)
	server.uploads = filesystem.NewUploadManager(server.files.Resolver(), t.TempDir())
	server.ops = operations.New(50)
	defer server.ops.Close(context.Background())
	initial := initializeReportedUpload(t, server)
	reader, writer := io.Pipe()
	defer writer.Close()
	request := httptest.NewRequest(http.MethodPut, "/api/fs/uploads/chunk?id="+initial.Upload.ID+"&offset=0&attempt_id="+initial.Operation.AttemptID, reader)
	request.ContentLength = 4
	finished := make(chan struct{})
	go func() { defer close(finished); server.uploadChunk(httptest.NewRecorder(), request) }()
	if _, err := writer.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	failed := reportFailedUpload(t, server, initial.Operation)
	if failed.State != operations.StateFailed {
		t.Fatalf("not failed: %#v", failed)
	}
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("writer remained blocked")
	}
	resumed := initializeReportedUpload(t, server)
	if resumed.Upload.Offset != 1 {
		t.Fatalf("lost partial upload: %#v", resumed.Upload)
	}
}
