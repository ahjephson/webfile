package auth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGIVEN_ProxyCredentialFile_WHEN_Loading_THEN_RequiresPrivateValidSecret(t *testing.T) {
	for _, scenario := range []struct {
		name, content string
		mode          os.FileMode
		allowed       bool
	}{
		{"valid", strings.Repeat("a", 64) + "\n", 0640, true},
		{"private", strings.Repeat("a", 64), 0600, true},
		{"public", strings.Repeat("a", 64), 0644, false},
		{"group-writable", strings.Repeat("a", 64), 0660, false},
		{"short", "Secret", 0600, false},
		{"invalid-hex", strings.Repeat("z", 64), 0600, false},
		{"oversized", strings.Repeat("a", 64) + strings.Repeat(" ", 128), 0600, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "credential")
			if err := os.WriteFile(path, []byte(scenario.content), scenario.mode); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, scenario.mode); err != nil {
				t.Fatal(err)
			}
			secret, err := LoadProxySecret(path)
			if (err == nil) != scenario.allowed {
				t.Fatalf("accepted=%v: %v", err == nil, err)
			}
			if scenario.allowed && secret != strings.Repeat("a", 64) {
				t.Fatal("credential changed")
			}
		})
	}
}

func TestGIVEN_SymlinkCredential_WHEN_Loading_THEN_RejectsIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(path, []byte(strings.Repeat("a", 64)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path, path+"-link"); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProxySecret(path + "-link"); err == nil {
		t.Fatal("accepted symlink credential")
	}
}
