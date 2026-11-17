package filesystem

import (
	"testing"
	"webfile/internal/rootedio"
)

func testRootedIO(t *testing.T) *rootedio.FS {
	t.Helper()
	disk := rootedio.New("/")
	t.Cleanup(func() { _ = disk.Close() })
	return disk
}
