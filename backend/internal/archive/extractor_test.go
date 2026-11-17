package archive

import (
	"context"
	"errors"
	"io/fs"
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
case "$(readlink "$out")" in "`+destinationRoot+`"/.webfile-extract-*) ;; *) exit 8;; esac
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

func TestValidateStageRepairsOnlyOwnerAccess(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode fs.FileMode
		dir  bool
		want fs.FileMode
	}{
		{"zero file", 0, false, 0o400},
		{"normal file", 0o644, false, 0o644},
		{"private file", 0o600, false, 0o600},
		{"read-only file", 0o444, false, 0o444},
		{"owner read-only file", 0o400, false, 0o400},
		{"executable file", 0o755, false, 0o755},
		{"unreadable executable", 0o111, false, 0o511},
		{"write-only file", 0o200, false, 0o600},
		{"group readable file", 0o040, false, 0o440},
		{"special bits", os.ModeSetuid | os.ModeSetgid | 0o111, false, os.ModeSetuid | os.ModeSetgid | 0o511},
		{"zero directory", 0, true, 0o500},
		{"unsearchable directory", 0o600, true, 0o700},
		{"unreadable directory", 0o100, true, 0o500},
		{"private directory", 0o700, true, 0o700},
		{"read-only directory", 0o555, true, 0o555},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stage := t.TempDir()
			defer removeStage(testRootedIO(t), stage)
			path := filepath.Join(stage, "entry")
			if tc.dir {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
				writeTestFile(t, filepath.Join(path, "child.txt"), "child")
			} else {
				writeTestFile(t, path, "file")
			}
			if err := os.Chmod(path, tc.mode); err != nil {
				t.Fatal(err)
			}
			count, err := validateStage(testRootedIO(t), stage)
			if err != nil || count != 1 {
				t.Fatalf("validation: count=%d error=%v", count, err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if got := info.Mode() & (fs.ModePerm | os.ModeSetuid | os.ModeSetgid); got != tc.want {
				t.Fatalf("mode=%v, want=%v", got, tc.want)
			}
		})
	}
}

func TestPublishRepairsUnsearchableNestedDirectoriesAndPreservesDestinationModes(t *testing.T) {
	for _, policy := range []ExistingFilePolicy{ExistingFilesSkip, ExistingFilesOverwrite} {
		t.Run(string(policy), func(t *testing.T) {
			root := t.TempDir()
			stage := filepath.Join(root, "stage")
			destination := filepath.Join(root, "destination")
			for _, base := range []string{stage, destination} {
				if err := os.MkdirAll(filepath.Join(base, "nested"), 0o700); err != nil {
					t.Fatal(err)
				}
				writeTestFile(t, filepath.Join(base, "nested", "existing.txt"), "existing")
			}
			writeTestFile(t, filepath.Join(stage, "nested", "new.txt"), "new")
			if err := os.Chmod(filepath.Join(destination, "nested", "existing.txt"), 0o640); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(filepath.Join(destination, "nested"), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(filepath.Join(stage, "nested"), 0); err != nil {
				t.Fatal(err)
			}
			result, err := publishStage(testRootedIO(t), context.Background(), stage, destination, true, policy, nil)
			if err != nil || result.Extracted+result.Skipped != 2 {
				t.Fatalf("publish: %#v, %v", result, err)
			}
			for path, want := range map[string]fs.FileMode{"nested": 0o750, "nested/existing.txt": 0o640} {
				info, err := os.Stat(filepath.Join(destination, path))
				if err != nil || info.Mode().Perm() != want {
					t.Fatalf("destination %s mode: %v, %v", path, info, err)
				}
			}
			if _, err := os.Stat(stage); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("staging not removed: %v", err)
			}
		})
	}
}

func TestPublishStageSkipsExistingRegularFiles(t *testing.T) {
	root := t.TempDir()
	stage := filepath.Join(root, "stage")
	destination := filepath.Join(root, "destination")
	if err := os.MkdirAll(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "existing.txt"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "added.txt"), []byte("added"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "existing.txt"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := publishStage(testRootedIO(t), context.Background(), stage, destination, true, ExistingFilesSkip, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Extracted != 1 || result.Skipped != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}
	content, err := os.ReadFile(filepath.Join(destination, "existing.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "old" {
		t.Fatalf("existing file changed to %q", content)
	}
	if _, err := os.Stat(filepath.Join(destination, "added.txt")); err != nil {
		t.Fatal(err)
	}
}

func TestPublishStageOverwritesExistingRegularFiles(t *testing.T) {
	root := t.TempDir()
	stage := filepath.Join(root, "stage")
	destination := filepath.Join(root, "destination")
	if err := os.MkdirAll(stage, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "existing.txt"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "existing.txt"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := publishStage(testRootedIO(t), context.Background(), stage, destination, true, ExistingFilesOverwrite, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Extracted != 1 || result.Skipped != 0 {
		t.Fatalf("unexpected result: %#v", result)
	}
	content, err := os.ReadFile(filepath.Join(destination, "existing.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "new" {
		t.Fatalf("existing file was not overwritten: %q", content)
	}
}

func TestPublishStageRejectsTypeConflictsBeforePublishing(t *testing.T) {
	root := t.TempDir()
	stage := filepath.Join(root, "stage")
	destination := filepath.Join(root, "destination")
	if err := os.MkdirAll(filepath.Join(stage, "conflict"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "conflict", "child.txt"), []byte("child"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stage, "new.txt"), []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "conflict"), []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := publishStage(testRootedIO(t), context.Background(), stage, destination, true, ExistingFilesOverwrite, nil); err == nil {
		t.Fatal("type conflict was accepted")
	}
	if _, err := os.Stat(filepath.Join(destination, "new.txt")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("new file was partially published: %v", err)
	}
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
		if progress.Phase == "extracting" && progress.PercentKnown {
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

func TestRecordParserTreatsTerminalBackspacesAsProgressBoundaries(t *testing.T) {
	var parser recordParser
	records := append(parser.Feed("  8%\b\b"), parser.Feed("\b\b   42%\b")...)
	records = append(records, parser.Feed("\b\b\b100%\b")...)
	records = append(records, parser.Flush())
	percentages := make([]int, 0)
	for _, record := range records {
		percentages = append(percentages, progressPercents(record)...)
	}
	if !reflect.DeepEqual(percentages, []int{8, 42, 100}) {
		t.Fatalf("percentages=%v records=%q", percentages, records)
	}
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

func TestRunToolDrainsPasswordErrorWithSlowProgressConsumer(t *testing.T) {
	root := t.TempDir()
	finished := filepath.Join(root, "finished")
	writeTestFile(t, filepath.Join(root, "input.zip"), "archive")
	tool := writeTool(t, `#!/bin/sh
set -eu
printf ' 64%%\n' >&2
head -c 66000 /dev/zero | tr '\000' x >&2
printf '\nWrong password\n' >&2
touch "`+finished+`"
exit 2
`)
	target := New(Config{Executable: tool}, testResolver(t, filesystem.Root{Name: "Files", Path: root}))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := target.runTool(ctx, filepath.Join(root, "input.zip"), root, "", func(progress Progress) {
		if !progress.PercentKnown || progress.Percent != 64 {
			return
		}
		for {
			if _, err := os.Stat(finished); err == nil {
				break
			}
			select {
			case <-ctx.Done():
				t.Error("archive tool did not finish writing its output")
				return
			case <-time.After(time.Millisecond):
			}
		}
		// Let the process exit while output is queued behind this callback.
		time.Sleep(50 * time.Millisecond)
	})
	if !errors.Is(err, ErrIncorrectPassword) {
		t.Fatalf("password error was lost while draining output: %v", err)
	}
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
	if err := publishDirectoryAbsent(testRootedIO(t), context.Background(), source, destination, info, &published, &done, 1, nil); err != nil {
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
