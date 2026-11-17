package archive

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"webfile/internal/filesystem"
)

func TestGIVEN_ArchiveReplacedAfterOpen_WHEN_Extracting_THEN_ToolReadsPinnedFile(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	archive := filepath.Join(root, "input.zip")
	sentinel := filepath.Join(outside, "sentinel")
	writeTestFile(t, archive, "original archive")
	writeTestFile(t, sentinel, "outside content")
	tool := writeTool(t, `#!/bin/sh
set -eu
mv "`+archive+`" "`+archive+`-original"
ln -s "`+sentinel+`" "`+archive+`"
out=""
input=""
for arg in "$@"; do
  case "$arg" in -o*) out="${arg#-o}";; /proc/self/fd/3) input="$arg";; esac
done
test -n "$input"
cat "$input" > "$out/content.txt"
`)
	target := New(Config{Executable: tool, ExtractArgs: []string{"x", "{archive}", "-o{destination}"}}, testResolver(t, filesystem.Root{Name: "Files", Path: root}))
	if _, err := target.Extract(context.Background(), "/Files/input.zip", "/Files/output", ""); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "output", "content.txt"))
	if err != nil || string(data) != "original archive" {
		t.Fatalf("tool followed replaced source: %q %v", data, err)
	}
}
