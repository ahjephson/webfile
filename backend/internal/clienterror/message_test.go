package clienterror

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"syscall"
	"testing"
)

func TestDiagnosticsBecomeFixedPublicMessages(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want Message
	}{
		{"missing", &os.PathError{Op: "lstat", Path: "/private/server/source", Err: fs.ErrNotExist}, NotFound},
		{"permission", &os.LinkError{Op: "rename", Old: "/private/source", New: "/private/target", Err: fs.ErrPermission}, Permission},
		{"disk", fmt.Errorf("write /private/stage: %w", syscall.ENOSPC), NoSpace},
		{"quota", syscall.EDQUOT, Quota},
		{"readonly", syscall.EROFS, ReadOnly},
		{"timeout", context.DeadlineExceeded, Timeout},
		{"short body", io.ErrUnexpectedEOF, Incomplete},
		{"archive output", errors.New("7z /usr/bin/7z: failed to extract /private/stage"), Unknown},
		{"wrapped safe error", fmt.Errorf("publish /private/target: %w", New("conflict", Exists)), Exists},
		{"unapproved public text", New("failure", Message("diagnostic /private/server/file")), Unknown},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := Text(test.err); got != string(test.want) {
				t.Fatalf("public message = %q", got)
			}
			if got := Stored(Text(test.err)); got != string(test.want) {
				t.Fatalf("stored message = %q", got)
			}
		})
	}
	if got := Stored("lstat /home/private/source: no such file or directory"); got != string(Unknown) {
		t.Fatalf("legacy diagnostic leaked: %s", got)
	}
}
