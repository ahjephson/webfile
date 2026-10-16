package archive

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"webfile/internal/filesystem"
)

func TestCreateFolderArchiveUsesToolProgressAndFilesystemExclusions(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "photos")
	if err := os.MkdirAll(filepath.Join(source, "private"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(source, "visible.txt"), "visible")
	writeTestFile(t, filepath.Join(source, "secret"), "excluded by name")
	writeTestFile(t, filepath.Join(source, "private", "hidden.txt"), "excluded by path")
	arguments := filepath.Join(t.TempDir(), "arguments.txt")
	tool := writeTool(t, `#!/bin/sh
set -eu
out=""
for arg in "$@"; do
  printf '%s\n' "$arg" >> "`+arguments+`"
  case "$arg" in *.partial.zip) out="$arg";; esac
done
printf ' 37%%\r100%%\n'
printf archive > "$out"
`)
	resolver := testResolver(t, filesystem.Root{
		Name: "Files", Path: root,
		ExcludedNames: []string{"secret"},
		ExcludedPaths: []string{"/photos/private"},
	})
	target := New(Config{Executable: tool, CreateArgs: []string{"create", "{archive}"}}, resolver)
	output := filepath.Join(t.TempDir(), "task.partial.zip")
	percentages := make([]int, 0)

	result, err := target.CreateFolderArchive(context.Background(), "/Files/photos", output, func(progress Progress) {
		if progress.PercentKnown {
			percentages = append(percentages, progress.Percent)
		}
	})

	if err != nil {
		t.Fatal(err)
	}
	if result.Files != 1 || result.Bytes != int64(len("visible")) {
		t.Fatalf("result = %#v", result)
	}
	if !reflect.DeepEqual(percentages, []int{37, 100}) {
		t.Fatalf("percentages = %v", percentages)
	}
	if _, err := os.Stat(output); err != nil {
		t.Fatalf("created archive was not retained: %v", err)
	}
	data, err := os.ReadFile(arguments)
	if err != nil {
		t.Fatal(err)
	}
	args := string(data)
	for _, expected := range []string{"-xr!photos/secret", "-x!photos/private", "-spd", "./photos"} {
		if !strings.Contains(args, expected+"\n") {
			t.Fatalf("arguments missing %q: %s", expected, args)
		}
	}
}

func TestCreateFolderArchiveRejectsIncludedSymbolicLink(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "folder")
	if err := os.Mkdir(source, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, "target.txt"), "target")
	if err := os.Symlink(filepath.Join(root, "target.txt"), filepath.Join(source, "link")); err != nil {
		t.Fatal(err)
	}
	target := New(Config{Executable: "/bin/false", CreateArgs: []string{"{archive}"}}, testResolver(t, filesystem.Root{Name: "Files", Path: root}))

	_, err := target.CreateFolderArchive(context.Background(), "/Files/folder", filepath.Join(t.TempDir(), "out.zip"), nil)

	if err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("error = %v", err)
	}
}
