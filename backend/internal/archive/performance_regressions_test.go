package archive

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPasswordInspectionMemoryDoesNotGrowWithListing(t *testing.T) {
	tool := writeTool(t, "#!/bin/sh\nawk 'BEGIN { for (i=0;i<200000;i++) printf \"Path = a-file-name-%08d\\nEncrypted = -\\n\", i }'\n")
	extractor := New(Config{Executable: tool, InspectArgs: []string{"l", "{archive}"}}, nil)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	required, err := extractor.PasswordRequired(context.Background(), "/unused.zip")
	runtime.ReadMemStats(&after)
	t.Logf("inspection allocated %.2f MiB", float64(after.TotalAlloc-before.TotalAlloc)/(1<<20))
	if err != nil || required {
		t.Fatalf("inspection: %v, %v", required, err)
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 32<<20 {
		t.Fatalf("listing allocated %.1f MiB", float64(allocated)/(1<<20))
	}
}

func TestCancelledPublicationSkipsValidationAndChecksButCleansStaging(t *testing.T) {
	parent := t.TempDir()
	stage := filepath.Join(parent, "stage")
	if err := os.Mkdir(stage, 0700); err != nil {
		t.Fatal(err)
	}
	// A permission repair would be observable even though publication is cancelled.
	file := filepath.Join(stage, "file")
	writeTestFile(t, file, "data")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	checked := false
	_, err := publishStage(ctx, stage, filepath.Join(parent, "destination"), false, ExistingFilesSkip, nil, func() error { checked = true; return nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if checked {
		t.Fatal("cancelled publication still performed validation checks")
	}
	if _, err := os.Stat(stage); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("private staging not cleaned: %v", err)
	}
}
