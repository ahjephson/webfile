package archive

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"webfile/internal/filesystem"
)

type CreateResult struct {
	Files int64
	Bytes int64
}

func (e *Extractor) CreateFolderArchive(ctx context.Context, virtualPath, outputPath string, report ProgressReporter) (CreateResult, error) {
	if !e.Enabled() || len(e.cfg.CreateArgs) == 0 {
		return CreateResult{}, errors.New("archive creation is not configured")
	}
	root, source, err := e.resolver.ResolveExisting(virtualPath)
	if err != nil {
		return CreateResult{}, err
	}
	info, err := os.Lstat(source)
	if err != nil {
		return CreateResult{}, err
	}
	if !info.IsDir() {
		return CreateResult{}, errors.New("archive source is not a directory")
	}
	if report != nil {
		report(Progress{Phase: "preparing", Message: "Checking folder contents"})
	}
	result, exclusions, err := preflightArchiveSource(ctx, root, source)
	if err != nil {
		return CreateResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o700); err != nil {
		return CreateResult{}, err
	}
	if err := os.Remove(outputPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return CreateResult{}, err
	}

	args := make([]string, 0, len(e.cfg.CreateArgs)+len(exclusions)+1)
	for _, configured := range e.cfg.CreateArgs {
		arg := strings.ReplaceAll(configured, "{archive}", outputPath)
		if strings.Contains(arg, "{source}") {
			return CreateResult{}, errors.New("archive source placeholders are not supported; WebFile appends the validated source")
		}
		args = append(args, arg)
	}
	for _, exclusion := range exclusions {
		args = append(args, "-xr!"+exclusion)
	}
	args = append(args, "."+string(os.PathSeparator)+filepath.Base(source))

	cmd := exec.CommandContext(ctx, e.cfg.Executable, args...)
	cmd.Dir = filepath.Dir(source)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return CreateResult{}, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return CreateResult{}, err
	}
	if err := cmd.Start(); err != nil {
		return CreateResult{}, err
	}
	if report != nil {
		report(Progress{Phase: "archiving", ItemsTotal: result.Files, BytesTotal: result.Bytes, Message: "Creating ZIP archive"})
	}

	chunks := make(chan string, 32)
	var readers sync.WaitGroup
	readers.Add(2)
	go streamChunks(stdout, chunks, &readers)
	go streamChunks(stderr, chunks, &readers)
	go func() {
		readers.Wait()
		close(chunks)
	}()
	var output strings.Builder
	var records recordParser
	lastPercent := -1
	for chunk := range chunks {
		appendLimited(&output, chunk, 8192)
		for _, record := range records.Feed(chunk) {
			lastPercent = reportArchiveProgress(record, lastPercent, result, report)
		}
	}
	lastPercent = reportArchiveProgress(records.Flush(), lastPercent, result, report)
	if err := cmd.Wait(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return CreateResult{}, ctxErr
		}
		return CreateResult{}, fmt.Errorf("archive tool failed: %w: %s", err, sanitizeToolError(output.String()))
	}
	if report != nil && lastPercent != 100 {
		report(Progress{Phase: "archiving", Percent: 100, PercentKnown: true, ItemsDone: result.Files, ItemsTotal: result.Files, BytesDone: result.Bytes, BytesTotal: result.Bytes, Message: "ZIP archive created"})
	}
	return result, nil
}

func reportArchiveProgress(record string, previous int, result CreateResult, report ProgressReporter) int {
	for _, percent := range progressPercents(record) {
		if percent == previous {
			continue
		}
		previous = percent
		if report != nil {
			report(Progress{
				Phase: "archiving", Percent: percent, PercentKnown: true,
				ItemsDone: result.Files * int64(percent) / 100, ItemsTotal: result.Files,
				BytesDone: result.Bytes * int64(percent) / 100, BytesTotal: result.Bytes,
				Message: "Creating ZIP archive",
			})
		}
	}
	return previous
}

func preflightArchiveSource(ctx context.Context, root filesystem.Root, source string) (CreateResult, []string, error) {
	excludedNames := make(map[string]struct{}, len(root.ExcludedNames))
	for _, name := range root.ExcludedNames {
		excludedNames[name] = struct{}{}
	}
	result := CreateResult{}
	exclusions := make([]string, 0)
	top := filepath.Base(source)
	err := filepath.WalkDir(source, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if current != source && archiveEntryExcluded(root, current, entry.Name(), excludedNames) {
			relative, err := filepath.Rel(source, current)
			if err != nil {
				return err
			}
			exclusions = append(exclusions, filepath.ToSlash(filepath.Join(top, relative)))
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("folder contains a symbolic link: %s", entry.Name())
		}
		if info.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("folder contains an unsupported special file: %s", entry.Name())
		}
		file, err := os.Open(current)
		if err != nil {
			return err
		}
		if err := file.Close(); err != nil {
			return err
		}
		result.Files++
		result.Bytes += info.Size()
		return nil
	})
	return result, exclusions, err
}

func archiveEntryExcluded(root filesystem.Root, candidate, name string, excludedNames map[string]struct{}) bool {
	if _, excluded := excludedNames[name]; excluded {
		return true
	}
	relative, err := filepath.Rel(root.Path, candidate)
	if err != nil || relative == "." {
		return false
	}
	virtualRelative := "/" + filepath.ToSlash(relative)
	for _, excludedPath := range root.ExcludedPaths {
		if virtualRelative == excludedPath || strings.HasPrefix(virtualRelative, excludedPath+"/") {
			return true
		}
	}
	return false
}
