package archive

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"webfile/internal/filesystem"
)

func TestExtractPublishesFromDestinationLocalStaging(t *testing.T) {
	sourceRoot := t.TempDir()
	destinationRoot := t.TempDir()
	writeTestFile(t, filepath.Join(sourceRoot, "sample.zip"), "archive")
	tool := writeTool(t, `#!/bin/sh
set -eu
out=""
for arg in "$@"; do case "$arg" in -o*) out="${arg#-o}";; esac; done
case "$out" in "`+destinationRoot+`"/.webfile-extract-*) ;; *) exit 8;; esac
mkdir -p "$out/folder"
printf ok > "$out/folder/file.txt"
`)
	resolver := testResolver(t, filesystem.Root{Name: "Downloads", Path: sourceRoot}, filesystem.Root{Name: "Media", Path: destinationRoot})
	target := New(Config{Executable: tool, ExtractArgs: []string{"x", "{archive}", "-o{destination}"}}, resolver)

	count, err := target.Extract(context.Background(), "/Downloads/sample.zip", "/Media/sample", "")

	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("count=%d", count)
	}
	data, err := os.ReadFile(filepath.Join(destinationRoot, "sample", "folder", "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "ok" {
		t.Fatalf("content=%q", data)
	}
	assertNoStaging(t, destinationRoot)
}

func TestExtractPreflightsAllConflictsBeforePublishing(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "sample.zip"), "archive")
	if err := os.Mkdir(filepath.Join(root, "output"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, "output", "conflict.txt"), "existing")
	tool := writeTool(t, `#!/bin/sh
set -eu
out=""
for arg in "$@"; do case "$arg" in -o*) out="${arg#-o}";; esac; done
printf replacement > "$out/conflict.txt"
printf new > "$out/new.txt"
`)
	target := New(Config{Executable: tool, ExtractArgs: []string{"x", "{archive}", "-o{destination}"}}, testResolver(t, filesystem.Root{Name: "Files", Path: root}))

	_, err := target.Extract(context.Background(), "/Files/sample.zip", "/Files/output", "")

	if err == nil || !strings.Contains(err.Error(), "conflict.txt") {
		t.Fatalf("error=%v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "output", "new.txt")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("new file was partially published: %v", statErr)
	}
	data, readErr := os.ReadFile(filepath.Join(root, "output", "conflict.txt"))
	if readErr != nil || string(data) != "existing" {
		t.Fatalf("existing file changed: %q, %v", data, readErr)
	}
	assertNoStaging(t, filepath.Join(root, "output"))
}

func TestExtractPromptsThroughStdinAndRetriesIncorrectPassword(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "sample.7z"), "archive")
	arguments := filepath.Join(root, "arguments.txt")
	tool := writeTool(t, `#!/bin/sh
set -eu
out=""
if [ "${1:-}" = inspect ]; then printf 'Encrypted = +\n'; exit 0; fi
printf '%s\n' "$@" >> "`+arguments+`"
for arg in "$@"; do case "$arg" in -o*) out="${arg#-o}";; esac; done
printf 'Enter password (will not be echoed):' >&2
IFS= read -r password
if [ "$password" != Correct ]; then printf '\nWrong password\n' >&2; exit 2; fi
mkdir -p "$out"
printf '\r 64%%\r100%%\n' >&2
printf ok > "$out/file.txt"
`)
	target := New(Config{Executable: tool, ExtractArgs: []string{"x", "{archive}", "-o{destination}"}, InspectArgs: []string{"inspect", "{archive}"}, InspectPasswordArg: "-p"}, testResolver(t, filesystem.Root{Name: "Files", Path: root}))
	incorrectValues := make([]bool, 0)
	passwords := []string{"Wrong", "Correct"}
	provider := func(_ context.Context, incorrect bool) (string, error) {
		incorrectValues = append(incorrectValues, incorrect)
		value := passwords[0]
		passwords = passwords[1:]
		return value, nil
	}
	percentages := make([]int, 0)

	count, err := target.ExtractInteractive(context.Background(), "/Files/sample.7z", "/Files/output", provider, func(progress Progress) {
		if progress.Phase == "extracting" {
			percentages = append(percentages, progress.Percent)
		}
	})

	if err != nil {
		t.Fatal(err)
	}
	if count != 1 || !reflect.DeepEqual(incorrectValues, []bool{false, true}) {
		t.Fatalf("count=%d incorrect=%v", count, incorrectValues)
	}
	if !reflect.DeepEqual(percentages, []int{64, 100}) {
		t.Fatalf("percentages=%v", percentages)
	}
	argumentText, err := os.ReadFile(arguments)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(argumentText), "Wrong") || strings.Contains(string(argumentText), "Correct") {
		t.Fatalf("password appeared in arguments: %s", argumentText)
	}
	assertNoStaging(t, root)
}

func TestExtractDoesNotMisreportFailureAfterPasswordPrompt(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "sample.7z"), "archive")
	tool := writeTool(t, `#!/bin/sh
set -eu
if [ "${1:-}" = inspect ]; then printf 'Encrypted = +\n'; exit 0; fi
printf 'Enter password (will not be echoed):' >&2
IFS= read -r password
if [ "$password" != Correct ]; then printf '\nWrong password\n' >&2; exit 2; fi
printf '\nI/O failure after decryption\n' >&2
exit 3
`)
	target := New(Config{Executable: tool, ExtractArgs: []string{"x", "{archive}"}, InspectArgs: []string{"inspect", "{archive}"}, InspectPasswordArg: "-p"}, testResolver(t, filesystem.Root{Name: "Files", Path: root}))
	prompts := 0
	_, err := target.ExtractInteractive(context.Background(), "/Files/sample.7z", "/Files/output", func(_ context.Context, incorrect bool) (string, error) {
		prompts++
		if incorrect {
			t.Fatal("ordinary extraction error was classified as an incorrect password")
		}
		return "Correct", nil
	}, nil)
	if err == nil || errors.Is(err, ErrIncorrectPassword) || !strings.Contains(err.Error(), "archive tool failed") {
		t.Fatalf("error=%v", err)
	}
	if prompts != 1 {
		t.Fatalf("prompts=%d", prompts)
	}
	assertNoStaging(t, root)
}

func TestPublishDirectoryFallbackPublishesRecursively(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "stage")
	destination := filepath.Join(root, "published")
	if err := os.MkdirAll(filepath.Join(source, "folder"), 0o750); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(source, "folder", "file.txt"), "content")
	info, err := os.Lstat(source)
	if err != nil {
		t.Fatal(err)
	}
	published := make([]publishedMove, 0)
	done := int64(0)
	if err := publishDirectoryAbsent(context.Background(), source, destination, info, &published, &done, 1, nil); err != nil {
		t.Fatal(err)
	}
	if done != 1 {
		t.Fatalf("done=%d", done)
	}
	if _, err := os.Stat(source); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source still exists: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(destination, "folder", "file.txt"))
	if err != nil || string(content) != "content" {
		t.Fatalf("content=%q error=%v", content, err)
	}
}

func TestExtractCancellationRemovesStaging(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, filepath.Join(root, "sample.zip"), "archive")
	tool := writeTool(t, `#!/bin/sh
set -eu
out=""
for arg in "$@"; do case "$arg" in -o*) out="${arg#-o}";; esac; done
printf partial > "$out/partial.txt"
sleep 10
`)
	target := New(Config{Executable: tool, ExtractArgs: []string{"x", "{archive}", "-o{destination}"}}, testResolver(t, filesystem.Root{Name: "Files", Path: root}))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := target.Extract(ctx, "/Files/sample.zip", "/Files/output", "")

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error=%v", err)
	}
	assertNoStaging(t, root)
}

func TestSplitArchiveRecordsAcceptsCarriageReturnsAndNewlines(t *testing.T) {
	records := make([]string, 0)
	err := scanRecords(strings.NewReader("one\rtwo\nthree\r\nfour"), func(record string) {
		if record != "" {
			records = append(records, record)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(records, []string{"one", "two", "three", "four"}) {
		t.Fatalf("records=%v", records)
	}
}

func testResolver(t *testing.T, roots ...filesystem.Root) *filesystem.Resolver {
	t.Helper()
	resolver, err := filesystem.NewResolver(roots)
	if err != nil {
		t.Fatal(err)
	}
	return resolver
}

func writeTool(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tool.sh")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertNoStaging(t *testing.T, root string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(root, ".webfile-extract-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("staging remains: %v", matches)
	}
}
