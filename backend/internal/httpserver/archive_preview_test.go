package httpserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"webfile/internal/archive"
	"webfile/internal/filesystem"
)

func TestArchivePreviewErrorsAndPasswords(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file.rar"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	resolver, err := filesystem.NewResolver([]filesystem.Root{{Name: "Files", Path: root}})
	if err != nil {
		t.Fatal(err)
	}
	tool := filepath.Join(root, "archive-tool")
	for _, tc := range []struct {
		name, output, code, message string
		status                      int
	}{
		{"password", "echo 'Enter password (will not be echoed):' >&2; exit 2", "archive_password_required", "Enter the archive password", 422},
		{"incorrect", "echo 'Wrong password?' >&2; exit 2", "archive_password_incorrect", "Incorrect password", 422},
		{"tool error", "echo '/private/server/path: bad archive' >&2; exit 2", "", "Could not open", 422},
		{"listing", "printf 'Path = hello.txt\\nSize = 12\\n'", "", "hello.txt", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(tool, []byte("#!/bin/sh\n"+tc.output+"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			server := &Server{extractor: archive.New(archive.Config{Executable: tool, InspectArgs: []string{"l", "-slt", "{archive}"}}, resolver), archivePreviewSlots: make(chan struct{}, 2)}
			password := ""
			if tc.name == "incorrect" {
				password = "wrong"
			}
			body, _ := json.Marshal(map[string]string{"path": "/Files/file.rar", "password": password})
			recorder := httptest.NewRecorder()
			server.previewArchive(recorder, httptest.NewRequest(http.MethodPost, "/api/fs/archive/preview", bytes.NewReader(body)))
			if recorder.Code != tc.status || !strings.Contains(recorder.Body.String(), tc.message) || strings.Contains(recorder.Body.String(), "/private/server") {
				t.Fatalf("response: %d %s", recorder.Code, recorder.Body.String())
			}
			if tc.code != "" && !strings.Contains(recorder.Body.String(), tc.code) {
				t.Fatalf("missing error code: %s", recorder.Body.String())
			}
			if recorder.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("preview response may be cached")
			}
		})
	}
}
