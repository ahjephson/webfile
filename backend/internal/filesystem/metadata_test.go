package filesystem

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestChmodAppliesAndReportsUnixSpecialBits(t *testing.T) {
	for _, tc := range []struct {
		name, text, octal string
		mode              os.FileMode
	}{
		{"ordinary", "0750", "0750", 0750},
		{"sticky", "1777", "1777", 0777 | os.ModeSticky},
		{"setgid", "2750", "2750", 0750 | os.ModeSetgid},
		{"setuid", "4755", "4755", 0755 | os.ModeSetuid},
		{"combined", "0o7750", "7750", 0750 | os.ModeSetuid | os.ModeSetgid | os.ModeSticky},
		{"clear", "0000", "0000", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			service, root := testService(t)
			path := filepath.Join(root, "entry")
			if err := os.Mkdir(path, 0700); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(path, 0700) })
			ctx := context.Background()
			if err := service.Chmod(ctx, "/Files/entry", tc.text); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			const mask = os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky
			if got := info.Mode() & mask; got != tc.mode {
				t.Fatalf("mode = %v, want %v", got, tc.mode)
			}
			details, err := service.Details(ctx, "/Files/entry")
			if err != nil {
				t.Fatal(err)
			}
			if details.ModeOctal != tc.octal {
				t.Fatalf("reported mode = %s, want %s", details.ModeOctal, tc.octal)
			}
			if err := service.Chmod(ctx, "/Files/entry", "0700"); err != nil {
				t.Fatal(err)
			}
			info, err = os.Stat(path)
			if err != nil || info.Mode()&mask != 0700 {
				t.Fatalf("special bits were not cleared: %v, %v", info, err)
			}
		})
	}
}
