package archive

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"webfile/internal/filesystem"
)

func TestRealMultipartExtraction(t *testing.T) {
	tool := realSevenZip(t)
	for _, scenario := range []string{"7z", "zip", "encrypted", "missing", "excluded", "symlink", "skipped", "changed", "keep", "corrupt"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "payload.txt")
			content := strings.Repeat("multipart payload\n", 100)
			writeTestFile(t, source, content)
			format := "7z"
			if scenario == "zip" {
				format = "zip"
			}
			name := "sample." + format
			args := []string{"a", "-t" + format, "-mx=0", "-v512b"}
			if scenario == "encrypted" {
				args = append(args, "-pCorrect", "-mhe=on")
			}
			args = append(args, filepath.Join(root, name), source)
			if output, err := exec.Command(tool, args...).CombinedOutput(); err != nil {
				t.Fatalf("create: %v: %s", err, output)
			}
			_ = os.Remove(source)
			parts, err := filepath.Glob(filepath.Join(root, name) + ".*")
			if err != nil || len(parts) < 3 {
				t.Fatalf("parts=%v, error=%v", parts, err)
			}
			cfg := Config{Executable: tool, ExtractArgs: []string{"x", "-y", "{archive}", "-o{destination}"}, InspectArgs: []string{"l", "-slt", "{archive}"}, InspectPasswordArg: "-p"}
			fsRoot := filesystem.Root{Name: "Files", Path: root}
			switch scenario {
			case "missing":
				_ = os.Remove(parts[1])
			case "excluded":
				fsRoot.ExcludedPaths = []string{"/" + filepath.Base(parts[1])}
			case "symlink":
				outside := filepath.Join(t.TempDir(), "volume")
				writeTestFile(t, outside, "private")
				_ = os.Remove(parts[1])
				if err := os.Symlink(outside, parts[1]); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				writeTestFile(t, parts[1], "broken")
			case "skipped":
				if err := os.Mkdir(filepath.Join(root, "output"), 0o700); err != nil {
					t.Fatal(err)
				}
				writeTestFile(t, filepath.Join(root, "output", "payload.txt"), "existing")
			}
			target := New(cfg, testResolver(t, fsRoot))
			password := ""
			if scenario == "encrypted" {
				password = "Correct"
			}
			if scenario == "7z" || scenario == "zip" || scenario == "encrypted" {
				listing, err := target.Preview(context.Background(), "/Files/"+name+".002", password)
				if err != nil || len(listing) != 1 || listing[0].Path != "payload.txt" {
					t.Fatalf("preview=%+v error=%v", listing, err)
				}
			}
			result, err := target.ExtractInteractiveWithOptions(context.Background(), "/Files/"+name+".001", "/Files/output", staticPasswordProvider(password), func(p Progress) {
				if scenario == "changed" && p.Phase == "publishing" {
					writeTestFile(t, parts[1], "replacement")
				}
			}, ExtractOptions{ExistingFiles: ExistingFilesSkip, DeleteArchive: scenario != "keep"})
			if scenario == "missing" || scenario == "excluded" || scenario == "symlink" || scenario == "corrupt" {
				if err == nil {
					t.Fatal("expected rejection")
				}
				if _, statErr := os.Stat(parts[0]); statErr != nil {
					t.Fatal("source was deleted")
				}
				if _, statErr := os.Stat(filepath.Join(root, "output")); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatal("failed extraction published output")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			retained := scenario == "skipped" || scenario == "changed" || scenario == "keep"
			if result.ArchiveDeleted == retained {
				t.Fatalf("result=%+v", result)
			}
			for _, part := range parts {
				_, err := os.Lstat(part)
				if retained && err != nil || !retained && !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("volume %s: %v", part, err)
				}
			}
			got, err := os.ReadFile(filepath.Join(root, "output", "payload.txt"))
			if scenario == "skipped" {
				content = "existing"
			}
			if err != nil || string(got) != content {
				t.Fatalf("payload mismatch: %v", err)
			}
		})
	}
}
