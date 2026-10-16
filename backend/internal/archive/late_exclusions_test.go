package archive

import (
	"archive/zip"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"webfile/internal/filesystem"
)

func TestFolderArchiveExcludesFilesArrivingAfterPreflight(t *testing.T) {
	for _, tc := range []struct {
		name, folder, excluded, late string
		byPath                       bool
	}{
		{"nested-name", "folder", "secret", "late/nested/secret", false},
		{"literal-wildcard", "folder", "secret*", "late/nested/secret*", false},
		{"root-name-match", "secret", "secret", "late/nested/secret", false},
		{"late-excluded-path", "folder", "/folder/private", "private/secret", true},
		{"literal-source-name", "folder*", "/folder*/private", "private/secret", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tool := realSevenZip(t)
			root := t.TempDir()
			source := filepath.Join(root, tc.folder)
			if err := os.Mkdir(source, 0700); err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, filepath.Join(source, "visible.txt"), "visible")
			writeTestFile(t, filepath.Join(source, "secret-public"), "also visible")
			if tc.byPath {
				if err := os.MkdirAll(filepath.Join(source, "late/nested/private"), 0700); err != nil {
					t.Fatal(err)
				}
				writeTestFile(t, filepath.Join(source, "late/nested/private/allowed.txt"), "allowed")
			}
			late := filepath.ToSlash(filepath.Join(tc.folder, tc.late))
			wrapper := writeTool(t, fmt.Sprintf("#!/bin/sh\nset -eu\nmkdir -p %q\nprintf hidden > %q\nexec %q \"$@\"\n", filepath.Dir(late), late, tool))
			configured := filesystem.Root{Name: "Files", Path: root}
			if tc.byPath {
				configured.ExcludedPaths = []string{tc.excluded}
			} else {
				configured.ExcludedNames = []string{tc.excluded}
			}
			creator := New(Config{Executable: wrapper, CreateArgs: []string{"a", "-tzip", "-y", "-bsp1", "-bso0", "-bse2", "-snl", "{archive}"}}, testResolver(t, configured))
			output := filepath.Join(t.TempDir(), "folder.zip")
			if _, err := creator.CreateFolderArchive(context.Background(), "/Files/"+tc.folder, output, nil); err != nil {
				t.Fatal(err)
			}
			reader, err := zip.OpenReader(output)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			found := map[string]bool{}
			for _, entry := range reader.File {
				found[entry.Name] = true
				if entry.Name == late || tc.byPath && strings.HasPrefix(entry.Name, tc.folder+"/private/") {
					t.Fatalf("excluded late entry archived: %s", entry.Name)
				}
			}
			for _, name := range []string{tc.folder + "/visible.txt", tc.folder + "/secret-public"} {
				if !found[name] {
					t.Fatalf("allowed file omitted: %s; archive=%v", name, found)
				}
			}
			if tc.byPath && !found[tc.folder+"/late/nested/private/allowed.txt"] {
				t.Fatalf("exact path exclusion suppressed a different subtree: %v", found)
			}
		})
	}
}
