package filesystem

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"testing"
)

// Historical in-memory representation, retained only for comparison.
type memoryUndoPlan struct {
	Kind  string
	Roots []UndoEntry
	Nodes map[string]undoStamp
}

// This represents one transferred folder with 10,000 regular-file children.
// Measure the real receipt layout and serialization, including path strings.
func BenchmarkUndoRecord10000Files(b *testing.B) {
	for b.Loop() {
		b.StopTimer()
		runtime.GC()
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		b.StartTimer()
		plan := memoryUndoPlan{Kind: "move", Roots: []UndoEntry{{Source: "/Files/source/photos", Target: "/Files/destination/photos"}}, Nodes: make(map[string]undoStamp, 10001)}
		plan.Nodes["/Files/destination/photos"] = undoStamp{Mode: os.ModeDir | 0755, Device: 2049, Inode: 100000}
		for index := range 10000 {
			name := fmt.Sprintf("/Files/destination/photos/holiday-2026-original-image-%05d.jpg", index)
			plan.Nodes[name] = undoStamp{Mode: 0644, Size: 4 << 20, Modified: 1794646800000000000, Changed: 1794646800000000000, Device: 2049, Inode: uint64(100001 + index)}
		}
		data, err := json.Marshal(plan)
		if err != nil {
			b.Fatal(err)
		}
		b.StopTimer()
		runtime.GC()
		runtime.GC()
		runtime.ReadMemStats(&after)
		b.ReportMetric(float64(after.HeapAlloc-before.HeapAlloc), "retained-B")
		b.ReportMetric(float64(len(data)), "journal-B")
		runtime.KeepAlive(plan)
		runtime.KeepAlive(data)
		b.StartTimer()
	}
	b.ReportAllocs()
}

// Candidate representation only: one identity receipt for a moved folder.
// This does not change the transfer or undo implementation.
func BenchmarkUndoFolderReceipt(b *testing.B) {
	for b.Loop() {
		stamp := undoStamp{Mode: os.ModeDir | 0755, Device: 2049, Inode: 100000}
		plan := memoryUndoPlan{Kind: "move", Roots: []UndoEntry{{Source: "/Files/source/photos", Target: "/Files/destination/photos", SourceParent: stamp}}, Nodes: map[string]undoStamp{"/Files/destination/photos": stamp}}
		data, err := json.Marshal(plan)
		if err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(len(data)), "journal-B")
		runtime.KeepAlive(plan)
		runtime.KeepAlive(data)
	}
	b.ReportAllocs()
}
