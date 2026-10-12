package archive

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"webfile/internal/filesystem"
)

func TestRealSevenZipEncryptedExtraction(t *testing.T) {
	tool := realSevenZip(t)
	root := t.TempDir()
	source := filepath.Join(root, "secret.txt")
	archivePath := filepath.Join(root, "secret.7z")
	if err := os.WriteFile(source, []byte("classified"), 0o600); err != nil {
		t.Fatal(err)
	}
	create := exec.Command(tool, "a", "-t7z", "-pCorrect", "-mhe=on", archivePath, source)
	if output, err := create.CombinedOutput(); err != nil {
		t.Fatalf("create encrypted archive: %v: %s", err, output)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}

	target := New(Config{
		Executable:         tool,
		ExtractArgs:        []string{"x", "-y", "-bsp1", "-bso0", "-bse2", "{archive}", "-o{destination}"},
		InspectArgs:        []string{"l", "-slt", "{archive}"},
		InspectPasswordArg: "-p",
	}, testResolver(t, filesystem.Root{Name: "Files", Path: root}))
	incorrectValues := make([]bool, 0, 2)
	passwords := []string{"Wrong", "Correct"}
	progressSeen := false

	result, err := target.ExtractInteractiveWithOptions(context.Background(), "/Files/secret.7z", "/Files/output", func(_ context.Context, incorrect bool) (string, error) {
		incorrectValues = append(incorrectValues, incorrect)
		if len(passwords) == 0 {
			return "", errors.New("unexpected extra password prompt")
		}
		password := passwords[0]
		passwords = passwords[1:]
		return password, nil
	}, func(progress Progress) {
		if progress.Phase == "extracting" {
			progressSeen = true
		}
	}, ExtractOptions{ExistingFiles: ExistingFilesSkip})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(incorrectValues, []bool{false, true}) {
		t.Fatalf("password prompts = %v", incorrectValues)
	}
	if !progressSeen {
		t.Fatal("no extraction progress was reported")
	}
	if result.Extracted != 1 || result.Skipped != 0 {
		t.Fatalf("result = %#v", result)
	}
	content, err := os.ReadFile(filepath.Join(root, "output", "secret.txt"))
	if err != nil || string(content) != "classified" {
		t.Fatalf("content = %q, error = %v", content, err)
	}
	assertNoStaging(t, root)
}

func TestRealSevenZipCreateFolderArchive(t *testing.T) {
	tool := realSevenZip(t)
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "folder"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(root, "folder", "one.txt"), "one")
	writeTestFile(t, filepath.Join(root, "folder", "two.txt"), "two")
	output := filepath.Join(t.TempDir(), "folder.zip")
	target := New(Config{
		Executable: tool,
		CreateArgs: []string{"a", "-tzip", "-y", "-bsp1", "-bso0", "-bse2", "-snl", "{archive}"},
	}, testResolver(t, filesystem.Root{Name: "Files", Path: root}))

	result, err := target.CreateFolderArchive(context.Background(), "/Files/folder", output, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Files != 2 || result.Bytes != 6 {
		t.Fatalf("result = %#v", result)
	}
	listing := exec.Command(tool, "l", "-slt", output)
	content, err := listing.CombinedOutput()
	if err != nil {
		t.Fatalf("list archive: %v: %s", err, content)
	}
	for _, expected := range []string{"folder/one.txt", "folder/two.txt"} {
		if !containsLineValue(string(content), "Path = "+expected) {
			t.Fatalf("archive listing does not contain %q: %s", expected, content)
		}
	}
}

func realSevenZip(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"7zz", "7z"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	t.Skip("7-Zip CLI is not installed")
	return ""
}

func containsLineValue(output, expected string) bool {
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) == expected {
			return true
		}
	}
	return false
}
