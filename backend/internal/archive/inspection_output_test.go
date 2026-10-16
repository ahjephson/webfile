package archive

import (
	"strings"
	"testing"
)

func TestInspectionOutputHandlesChunkBoundariesAndBoundedMalformedRecords(t *testing.T) {
	for _, text := range []string{"EnCrYpTeD = +", "WRONG PASSWORD", "Incorrect Password", "Data error in encrypted file", "Encrypted archive"} {
		t.Run(text, func(t *testing.T) {
			output := &inspectionOutput{}
			for i := range text {
				if _, err := output.Write([]byte{text[i]}); err != nil {
					t.Fatal(err)
				}
			}
			output.flush()
			if !output.required {
				t.Fatalf("missed password marker %q", text)
			}
		})
	}
	output := &inspectionOutput{}
	_, _ = output.Write([]byte(strings.Repeat("x", 1<<20)))
	if len(output.line) > 64<<10 || len(output.tail) > 8192 {
		t.Fatal("unbounded tool output")
	}
	_, _ = output.Write([]byte("\nEncrypted = -\n"))
	output.flush()
	if output.required {
		t.Fatal("unencrypted archive was misidentified")
	}
	_, _ = output.Write([]byte("Encrypted = +\r"))
	if !output.required {
		t.Fatal("long earlier record masked encrypted entry")
	}
}
