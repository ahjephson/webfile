package filesystem

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestSearchStreamsScopedMatchesWithoutFollowingLinksOrExclusions(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	for _, dir := range []string{"nested", "excluded", "private", ".hidden", ".webfile-extract-stage"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"nested/Report.TXT", "excluded/report.txt", "private/report.txt", ".hidden/report.txt", ".webfile-extract-stage/report.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("data"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(outside, "report.txt"), []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	resolver, _ := NewResolver([]Root{{Name: "Files", Path: root, ExcludedNames: []string{"excluded"}, ExcludedPaths: []string{"/private"}}})
	service := NewService(resolver, 1024)
	defer service.Close()
	var matches []string
	done := false
	err := service.Search(context.Background(), SearchRequest{Path: "/Files", Query: "*.txt", Wildcard: true, Kind: "files"}, func(update SearchUpdate) error {
		if update.Entry != nil {
			matches = append(matches, update.Entry.Path)
		}
		done = update.Done
		return nil
	})
	if err != nil || !done || len(matches) != 1 || matches[0] != "/Files/nested/Report.TXT" {
		t.Fatal(matches, done, err)
	}
	matches = nil
	err = service.Search(context.Background(), SearchRequest{Path: "/Files", Query: "report", CaseSensitive: true, ShowHidden: true}, func(update SearchUpdate) error {
		if update.Entry != nil {
			matches = append(matches, update.Entry.Path)
		}
		return nil
	})
	if err != nil || len(matches) != 1 || matches[0] != "/Files/.hidden/report.txt" {
		t.Fatal(matches, err)
	}
}

func TestSearchCancellationAndLimitsLeavePartialResultsAndReleaseSlot(t *testing.T) {
	service, root := testService(t)
	for i := 0; i < 1001; i++ {
		if err := os.WriteFile(filepath.Join(root, newSearchName(i)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	var terminal SearchUpdate
	count := 0
	if err := service.Search(context.Background(), SearchRequest{Path: "/Files", Query: "file"}, func(u SearchUpdate) error {
		if u.Entry != nil {
			count++
		}
		terminal = u
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if count != 1000 || !terminal.Truncated || !terminal.Done {
		t.Fatal(count, terminal)
	}
	ctx, cancel := context.WithCancel(context.Background())
	err := service.Search(ctx, SearchRequest{Path: "/Files", Query: "file"}, func(u SearchUpdate) error { cancel(); return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := service.Search(context.Background(), SearchRequest{Path: "/Files", Query: "absent"}, func(SearchUpdate) error { return nil }); err != nil {
		t.Fatal(err)
	}
	for _, r := range []SearchRequest{{Query: ""}, {Query: "[", Wildcard: true}, {Query: "x", Kind: "unknown"}} {
		if r.Validate() == nil {
			t.Fatal("invalid request accepted", r)
		}
	}
}

func newSearchName(index int) string { return fmt.Sprintf("file-%04d.txt", index) }
