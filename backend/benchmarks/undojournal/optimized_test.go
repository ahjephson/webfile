package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestCaptureRoundTripAndPartialBulkBatch(t *testing.T) {
	root := t.TempDir()
	fixture := filepath.Join(root, "tree")
	if err := createFixture(fixture, 33); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(root, "input.bin")
	var export measurement
	if err := optimized("capture", "binary", "", fixture, input, 8, 16, 8192, &export); err != nil {
		t.Fatal(err)
	}
	var expected, actual []receipt
	if err := walk(fixture, 8, func(entry receipt) error { expected = append(expected, entry); return nil }); err != nil {
		t.Fatal(err)
	}
	if err := readBinary(input, func(entry receipt) error { actual = append(actual, entry); return nil }); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatal("binary journal changed metadata")
	}
	for _, writer := range []string{"indexed-bulk", "append-sql", "binary"} {
		t.Run(writer, func(t *testing.T) {
			output := filepath.Join(root, writer+".journal")
			var recorded, validated measurement
			if err := optimized("capture", writer, input, "", output, 8, 16, 8192, &recorded); err != nil {
				t.Fatal(err)
			}
			if recorded.Entries != 34 || recorded.Checksum != export.Checksum {
				t.Fatal("capture omitted or changed entries", recorded)
			}
			if writer == "indexed-bulk" {
				db, err := openJournal(output, false, 8192)
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				if err := validate(db, fixture, 8, &validated); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := validateStream(writer, fixture, output, 8, 8192, &validated); err != nil {
					t.Fatal(err)
				}
				if validated.Checksum != export.Checksum {
					t.Fatal("validation changed receipts")
				}
			}
			if validated.Entries != 34 {
				t.Fatal("validation missed entries", validated)
			}
		})
	}
	periodic := filepath.Join(root, "periodic.bin")
	if err := optimized("capture", "binary", input, "", periodic, 8, 16, 8192, &measurement{}, 16); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	synced, err := os.ReadFile(periodic)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, synced) {
		t.Fatal("periodic sync changed journal")
	}
	data, err := os.ReadFile(input)
	if err != nil {
		t.Fatal(err)
	}
	truncated := filepath.Join(root, "truncated.bin")
	if err := os.WriteFile(truncated, data[:len(data)-1], 0600); err != nil {
		t.Fatal(err)
	}
	if err := readBinary(truncated, func(receipt) error { return nil }); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal("truncated journal accepted", err)
	}
}

func TestStreamValidationRejectsFilesystemChanges(t *testing.T) {
	for _, change := range []string{"edit", "add", "missing"} {
		t.Run(change, func(t *testing.T) {
			root := t.TempDir()
			fixture := filepath.Join(root, "tree")
			if err := createFixture(fixture, 2); err != nil {
				t.Fatal(err)
			}
			journal := filepath.Join(root, "journal.bin")
			if err := optimized("capture", "binary", "", fixture, journal, 8, 16, 8192, &measurement{}); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(fixture, "holiday-2026-original-image-000000001.jpg")
			var err error
			switch change {
			case "edit":
				err = os.WriteFile(file, []byte("changed"), 0600)
			case "add":
				err = os.WriteFile(filepath.Join(fixture, "extra"), nil, 0600)
			case "missing":
				err = os.Remove(file)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := validateStream("binary", fixture, journal, 8, 8192, &measurement{}); err == nil {
				t.Fatal("changed fixture accepted")
			}
		})
	}
}
