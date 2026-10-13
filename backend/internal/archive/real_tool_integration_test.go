package archive

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"webfile/internal/filesystem"
)

func TestRealSevenZipZeroPermissionsReadableThroughWebFile(t *testing.T) {
	tool := realSevenZip(t)
	encoded, err := os.ReadFile("testdata/zero-permissions.7z.b64")
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name       string
		attributes uint32
		listing    string
		readOnly   bool
	}{
		{"stored-zero-unix-mode", 0x80008021, "RA ----------", true},
		{"windows-read-only", 0x21, "RA", true},
		{"unix-executable", 0x81ed8020, "A -rwxr-xr-x", false},
		{"unix-read-only", 0x81248021, "RA -r--r--r--", true},
		{"missing-permissions", 0, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			data := append([]byte(nil), fixture...)
			// Last FilesInfo property: WinAttributes, all defined, inline.
			if tc.attributes == 0 {
				// Remove the entire property, keeping the two header end markers.
				data = append(data[:len(data)-14], 0, 0)
				binary.LittleEndian.PutUint64(data[20:28], binary.LittleEndian.Uint64(data[20:28])-12)
			} else {
				binary.LittleEndian.PutUint32(data[len(data)-6:], tc.attributes)
			}
			headerOffset := 32 + int(binary.LittleEndian.Uint64(data[12:20]))
			binary.LittleEndian.PutUint32(data[28:32], crc32.ChecksumIEEE(data[headerOffset:]))
			binary.LittleEndian.PutUint32(data[8:12], crc32.ChecksumIEEE(data[12:32]))
			archivePath := filepath.Join(root, "permissions.7z")
			if err := os.WriteFile(archivePath, data, 0o600); err != nil {
				t.Fatal(err)
			}
			listing, err := exec.Command(tool, "l", "-slt", archivePath).CombinedOutput()
			if err != nil {
				t.Fatalf("list fixture: %v: %s", err, listing)
			}
			attributes := strings.TrimSpace("Attributes = " + tc.listing)
			if tc.attributes != 0 && !containsLineValue(string(listing), attributes) {
				t.Fatalf("fixture missing %q: %s", attributes, listing)
			}
			if tc.attributes == 0 && strings.Contains(string(listing), "Attributes =") {
				t.Fatalf("fixture unexpectedly has attributes: %s", listing)
			}
			raw := filepath.Join(root, "raw")
			if output, err := exec.Command(tool, "x", "-y", "-o"+raw, archivePath).CombinedOutput(); err != nil {
				t.Fatalf("direct extraction: %v: %s", err, output)
			}
			rawInfo, err := os.Stat(filepath.Join(raw, "locked", "zero.txt"))
			if err != nil {
				t.Fatal(err)
			}
			if tc.name == "stored-zero-unix-mode" && rawInfo.Mode().Perm() != 0 {
				t.Fatalf("fixture no longer reproduces mode 0000: %v", rawInfo.Mode())
			}
			resolver := testResolver(t, filesystem.Root{Name: "Files", Path: root})
			target := New(Config{
				Executable:  tool,
				ExtractArgs: []string{"x", "-y", "{archive}", "-o{destination}"},
			}, resolver)
			count, err := target.Extract(context.Background(), "/Files/permissions.7z", "/Files/output", "")
			if err != nil || count != 1 {
				t.Fatalf("extract: count=%d error=%v", count, err)
			}
			service := filesystem.NewService(resolver, 1024)
			defer service.Close()
			entries, err := service.List(context.Background(), "/Files/output/locked")
			if err != nil || len(entries) != 1 || entries[0].CanRead == nil || !*entries[0].CanRead {
				t.Fatalf("WebFile listing: %#v, %v", entries, err)
			}
			content, err := service.ReadText(context.Background(), "/Files/output/locked/zero.txt")
			if err != nil || content.Content != "readable" {
				t.Fatalf("WebFile read: %#v, %v", content, err)
			}
			file, _, err := service.OpenDownload(context.Background(), "/Files/output/locked/zero.txt")
			if err != nil {
				t.Fatalf("WebFile download: %v", err)
			}
			file.Close()
			info, err := os.Stat(filepath.Join(root, "output", "locked", "zero.txt"))
			if err != nil || info.Mode().Perm() != rawInfo.Mode().Perm()|0o400 {
				t.Fatalf("file mode: %v, raw=%v, error=%v", info, rawInfo.Mode(), err)
			}
			if tc.readOnly && info.Mode().Perm()&0o222 != 0 {
				t.Fatalf("read-only intent lost: %v", info.Mode())
			}
			assertNoStaging(t, root)
		})
	}
}

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
