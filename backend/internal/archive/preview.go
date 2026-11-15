package archive

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

var ErrPreviewLimit = errors.New("archive listing exceeds preview limits")
var ErrPreviewToolUnavailable = errors.New("archive preview tool is unavailable")

type PreviewEntry struct {
	Path      string `json:"path"`
	IsDir     bool   `json:"is_dir"`
	Size      int64  `json:"size"`
	Modified  string `json:"modified,omitempty"`
	Encrypted bool   `json:"encrypted"`
}

// Preview lists metadata only. It never extracts archive members or writes passwords.
func (e *Extractor) Preview(ctx context.Context, virtualPath, password string) ([]PreviewEntry, error) {
	root, absolute, err := e.resolver.ResolveExisting(virtualPath)
	if err != nil {
		return nil, err
	}
	rootFS, err := os.OpenRoot(root.Path)
	if err != nil {
		return nil, err
	}
	defer rootFS.Close()
	relative, err := filepath.Rel(root.Path, absolute)
	if err != nil {
		return nil, err
	}
	// Avoid blocking on a FIFO, including one swapped in after resolution.
	file, err := rootFS.OpenFile(relative, os.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("archive is not a regular file")
	}
	args := make([]string, 0, len(e.cfg.InspectArgs)+2)
	for _, arg := range e.cfg.InspectArgs {
		if strings.Contains(arg, "{password}") {
			return nil, errors.New("archive password placeholders are not supported")
		}
		args = append(args, strings.ReplaceAll(arg, "{archive}", "/proc/self/fd/3"))
	}
	if len(args) == 0 {
		return nil, errors.New("archive inspection is not configured")
	}
	args = append(args, "-ba", "-sccUTF-8")
	executable, err := exec.LookPath(e.cfg.Executable)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPreviewToolUnavailable, err)
	}
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.WaitDelay = 2 * time.Second
	cmd.ExtraFiles = []*os.File{file}
	cmd.Stdin = strings.NewReader(password + "\n")
	output, detail := &previewOutput{}, &previewOutput{}
	cmd.Stdout, cmd.Stderr = output, detail
	err = cmd.Run()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if output.exceeded || detail.exceeded {
		return nil, ErrPreviewLimit
	}
	diagnostic := output.String() + "\n" + detail.String()
	if err != nil {
		if passwordPromptOutput(strings.ToLower(diagnostic)) || incorrectPasswordOutput(strings.ToLower(diagnostic)) {
			if password == "" {
				return nil, ErrPasswordRequired
			}
			return nil, ErrIncorrectPassword
		}
		if password != "" {
			diagnostic = strings.ReplaceAll(diagnostic, password, "[redacted]")
		}
		return nil, fmt.Errorf("archive preview failed: %w: %s", err, sanitizeToolError(diagnostic))
	}
	return parsePreview(output.String())
}

type previewOutput struct {
	buffer   bytes.Buffer
	exceeded bool
}

func (b *previewOutput) String() string { return b.buffer.String() }

func (b *previewOutput) Write(data []byte) (int, error) {
	const limit = 8 << 20
	if b.buffer.Len()+len(data) > limit {
		b.exceeded = true
		return 0, ErrPreviewLimit
	}
	return b.buffer.Write(data)
}

func parsePreview(listing string) ([]PreviewEntry, error) {
	entries := make([]PreviewEntry, 0)
	var entry *PreviewEntry
	flush := func() error {
		if entry == nil {
			return nil
		}
		if len(entries) >= 50000 {
			return ErrPreviewLimit
		}
		entries = append(entries, *entry)
		entry = nil
		return nil
	}
	for _, line := range strings.Split(strings.ReplaceAll(listing, "\r\n", "\n"), "\n") {
		if line == "" {
			if err := flush(); err != nil {
				return nil, err
			}
			continue
		}
		key, value, ok := strings.Cut(line, " = ")
		if !ok {
			continue
		}
		switch key {
		case "Path":
			if err := flush(); err != nil {
				return nil, err
			}
			entry = &PreviewEntry{Path: value}
		case "Size":
			if entry != nil {
				entry.Size, _ = strconv.ParseInt(value, 10, 64)
				if entry.Size < 0 {
					entry.Size = 0
				}
			}
		case "Folder":
			if entry != nil {
				entry.IsDir = value == "+"
			}
		case "Attributes":
			if entry != nil && strings.HasPrefix(value, "D") {
				entry.IsDir = true
			}
		case "Modified":
			if entry != nil {
				entry.Modified = value
			}
		case "Encrypted":
			if entry != nil {
				entry.Encrypted = value == "+"
			}
		}
	}
	if err := flush(); err != nil {
		return nil, err
	}
	return entries, nil
}
