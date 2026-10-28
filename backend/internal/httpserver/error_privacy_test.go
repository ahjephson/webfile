package httpserver

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"webfile/internal/clienterror"
	"webfile/internal/filesystem"
)

func TestHTTPErrorResponsesDoNotExposeDiagnostics(t *testing.T) {
	for _, test := range []struct {
		err     error
		status  int
		message clienterror.Message
	}{
		{&os.PathError{Op: "open", Path: "/private/server/file", Err: fs.ErrPermission}, 403, clienterror.Permission},
		{&os.PathError{Op: "lstat", Path: "/private/server/file", Err: fs.ErrNotExist}, 404, clienterror.NotFound},
		{fmt.Errorf("publish /private/server/file: %w", filesystem.ErrConflict), 412, clienterror.Exists},
		{fmt.Errorf("resolve /private/server/file: %w", filesystem.ErrOutsideRoot), 400, clienterror.InvalidPath},
		{errors.New("7z /private/server/file: tool output"), 500, clienterror.Unknown},
	} {
		response := httptest.NewRecorder()
		writeError(response, test.err)
		if response.Code != test.status || !strings.Contains(response.Body.String(), string(test.message)) || strings.Contains(response.Body.String(), "/private") {
			t.Fatalf("HTTP %d: %s", response.Code, response.Body.String())
		}
	}
}
