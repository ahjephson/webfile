package archive

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"webfile/internal/taskcontrol"

	"webfile/internal/clienterror"
	"webfile/internal/filesystem"

	"golang.org/x/sys/unix"
)

var (
	ErrPasswordRequired  = errors.New("archive password is required")
	ErrIncorrectPassword = errors.New("incorrect archive password")
)

type Config struct {
	Executable         string
	CreateArgs         []string
	ExtractArgs        []string
	InspectArgs        []string
	InspectPasswordArg string
}

type Progress struct {
	Phase        string
	Percent      int
	PercentKnown bool
	ItemsDone    int64
	ItemsTotal   int64
	BytesDone    int64
	BytesTotal   int64
	Message      string
}

type ExistingFilePolicy string

const (
	ExistingFilesFail      ExistingFilePolicy = ""
	ExistingFilesSkip      ExistingFilePolicy = "skip"
	ExistingFilesOverwrite ExistingFilePolicy = "overwrite"
)

type ExtractOptions struct {
	ExistingFiles ExistingFilePolicy
	DeleteArchive bool
}

type ExtractResult struct {
	Extracted       int
	Skipped         int
	ArchiveDeleted  bool
	ArchiveRetained string
}

type PasswordProvider func(context.Context, bool) (string, error)
type ProgressReporter func(Progress)

type Extractor struct {
	cfg      Config
	resolver *filesystem.Resolver
}

func New(cfg Config, resolver *filesystem.Resolver) *Extractor {
	return &Extractor{cfg: cfg, resolver: resolver}
}

func (e *Extractor) Enabled() bool {
	return strings.TrimSpace(e.cfg.Executable) != ""
}

func (e *Extractor) Extract(ctx context.Context, archivePath, destinationPath, password string) (int, error) {
	return e.ExtractInteractive(ctx, archivePath, destinationPath, staticPasswordProvider(password), nil)
}

func (e *Extractor) ExtractWithProgress(ctx context.Context, archivePath, destinationPath, password string, report func(done, total int64, message string)) (int, error) {
	return e.ExtractInteractive(ctx, archivePath, destinationPath, staticPasswordProvider(password), func(progress Progress) {
		if report != nil {
			report(progress.ItemsDone, progress.ItemsTotal, progress.Message)
		}
	})
}

func staticPasswordProvider(password string) PasswordProvider {
	return func(_ context.Context, incorrect bool) (string, error) {
		if password == "" || incorrect {
			return "", ErrPasswordRequired
		}
		return password, nil
	}
}

func (e *Extractor) ExtractInteractive(ctx context.Context, archivePath, destinationPath string, password PasswordProvider, report ProgressReporter) (int, error) {
	result, err := e.ExtractInteractiveWithOptions(ctx, archivePath, destinationPath, password, report, ExtractOptions{})
	return result.Extracted, err
}

func (e *Extractor) ExtractInteractiveWithOptions(ctx context.Context, archivePath, destinationPath string, password PasswordProvider, report ProgressReporter, options ExtractOptions) (ExtractResult, error) {
	if !e.Enabled() {
		return ExtractResult{}, errors.New("archive extraction is not configured")
	}
	_, archiveAbs, err := e.resolver.ResolveExisting(archivePath)
	if err != nil {
		return ExtractResult{}, err
	}
	var sourceFS *os.Root
	var sourceInfo os.FileInfo
	var sourceRelative string
	if options.DeleteArchive {
		archiveRoot, sourceAbs, resolveErr := e.resolver.ResolveEntry(archivePath)
		if resolveErr != nil {
			return ExtractResult{}, resolveErr
		}
		sourceFS, err = os.OpenRoot(archiveRoot.Path)
		if err != nil {
			return ExtractResult{}, err
		}
		defer sourceFS.Close()
		sourceRelative, err = filepath.Rel(archiveRoot.Path, sourceAbs)
		if err != nil {
			return ExtractResult{}, err
		}
		sourceInfo, err = sourceFS.Lstat(sourceRelative)
		if err != nil {
			return ExtractResult{}, err
		}
	}
	destinationRoot, destinationAbs, err := e.resolver.ResolveExisting(destinationPath)
	destinationExists := err == nil
	if errors.Is(err, fs.ErrNotExist) {
		destinationRoot, destinationAbs, err = e.resolver.ResolveForCreate(destinationPath)
	}
	if err != nil {
		return ExtractResult{}, err
	}
	if destinationExists {
		info, statErr := os.Stat(destinationAbs)
		if statErr != nil {
			return ExtractResult{}, statErr
		}
		if !info.IsDir() {
			return ExtractResult{}, errors.New("extraction destination is not a directory")
		}
	}

	passwordValue := ""
	passwordRequired, err := e.PasswordRequired(ctx, archiveAbs)
	if err != nil {
		return ExtractResult{}, err
	}
	if passwordRequired {
		if password == nil {
			return ExtractResult{}, ErrPasswordRequired
		}
		passwordValue, err = password(ctx, false)
		if err != nil {
			return ExtractResult{}, err
		}
	}
	incorrect := false
	for {
		if err := taskcontrol.Wait(ctx); err != nil {
			return ExtractResult{}, err
		}
		stageParent := filepath.Dir(destinationAbs)
		if destinationExists {
			stageParent = destinationAbs
		}
		stage, err := os.MkdirTemp(stageParent, ".webfile-extract-")
		if err != nil {
			return ExtractResult{}, err
		}
		runErr := e.runTool(ctx, archiveAbs, stage, passwordValue, report)
		if errors.Is(runErr, ErrPasswordRequired) {
			if cleanupErr := removeStage(stage); cleanupErr != nil {
				return ExtractResult{}, errors.Join(runErr, fmt.Errorf("clean password attempt: %w", cleanupErr))
			}
			if password == nil {
				return ExtractResult{}, ErrPasswordRequired
			}
			passwordValue, err = password(ctx, false)
			if err != nil {
				return ExtractResult{}, err
			}
			continue
		}
		if errors.Is(runErr, ErrIncorrectPassword) {
			if cleanupErr := removeStage(stage); cleanupErr != nil {
				return ExtractResult{}, errors.Join(runErr, fmt.Errorf("clean password attempt: %w", cleanupErr))
			}
			incorrect = true
			if password == nil {
				return ExtractResult{}, ErrIncorrectPassword
			}
			passwordValue, err = password(ctx, incorrect)
			if err != nil {
				return ExtractResult{}, err
			}
			continue
		}
		if runErr != nil {
			_ = removeStage(stage)
			return ExtractResult{}, runErr
		}
		result, err := publishStage(ctx, stage, destinationAbs, destinationExists, options.ExistingFiles, report, func() error {
			return validateExtractionPathsContext(ctx, stage, destinationRoot, destinationAbs)
		})
		if err != nil || !options.DeleteArchive {
			return result, err
		}
		if result.Skipped > 0 {
			result.ArchiveRetained = "archive kept because files were skipped"
			return result, nil
		}
		if err := taskcontrol.Wait(ctx); err != nil {
			return result, err
		}
		// Delete only the unchanged source, through its configured root. Never
		// recursively remove a replacement directory or follow a replacement link.
		current, err := sourceFS.Lstat(sourceRelative)
		if err != nil || !current.Mode().IsRegular() || !os.SameFile(sourceInfo, current) ||
			current.Size() != sourceInfo.Size() || !current.ModTime().Equal(sourceInfo.ModTime()) {
			result.ArchiveRetained = "archive kept because the source changed, is unavailable, or is not a regular file"
			return result, nil
		}
		if err := sourceFS.Remove(sourceRelative); err != nil {
			result.ArchiveRetained = "archive kept because it could not be deleted"
			return result, nil
		}
		result.ArchiveDeleted = true
		return result, nil
	}
}

func validateExtractionPaths(stage string, root filesystem.Root, destination string) error {
	return validateExtractionPathsContext(context.Background(), stage, root, destination)
}

func validateExtractionPathsContext(ctx context.Context, stage string, root filesystem.Root, destination string) error {
	return filepath.WalkDir(stage, func(current string, entry fs.DirEntry, walkErr error) error {
		if err := taskcontrol.Wait(ctx); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(stage, current)
		if err != nil {
			return err
		}
		if err := root.ValidatePath(filepath.Join(destination, relative)); err != nil {
			return fmt.Errorf("archive destination is excluded or outside its root: %s: %w", relative, err)
		}
		return nil
	})
}

func (e *Extractor) PasswordRequired(ctx context.Context, archivePath string) (bool, error) {
	if len(e.cfg.InspectArgs) == 0 {
		return false, nil
	}
	args := make([]string, 0, len(e.cfg.InspectArgs)+1)
	for _, configured := range e.cfg.InspectArgs {
		args = append(args, strings.ReplaceAll(configured, "{archive}", archivePath))
	}
	if e.cfg.InspectPasswordArg != "" {
		args = append(args, e.cfg.InspectPasswordArg+"__webfile_detection__")
	}
	cmd := exec.CommandContext(ctx, e.cfg.Executable, args...)
	cmd.Stdin = strings.NewReader("")
	output := &inspectionOutput{}
	cmd.Stdout, cmd.Stderr = output, output
	err := taskcontrol.Run(ctx, cmd)
	output.flush()
	if ctxErr := ctx.Err(); ctxErr != nil {
		return false, ctxErr
	}
	if output.required {
		return true, nil
	}
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return false, ctxErr
		}
		return false, fmt.Errorf("archive inspection failed: %w: %s", err, sanitizeToolError(string(output.tail)))
	}
	return false, nil
}

func (e *Extractor) runTool(ctx context.Context, archivePath, stage, password string, report ProgressReporter) error {
	args := make([]string, 0, len(e.cfg.ExtractArgs)+1)
	for _, configured := range e.cfg.ExtractArgs {
		arg := strings.ReplaceAll(configured, "{archive}", archivePath)
		arg = strings.ReplaceAll(arg, "{destination}", stage)
		if strings.Contains(arg, "{password}") {
			return errors.New("archive password placeholders are not supported; configure a prompt-only password switch")
		}
		args = append(args, arg)
	}
	runContext, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(runContext, e.cfg.Executable, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := taskcontrol.Start(ctx, cmd); err != nil {
		return err
	}
	if report != nil {
		report(Progress{Phase: "extracting", Message: "Extracting archive — files will appear when complete"})
	}
	if password != "" {
		if _, err := io.WriteString(stdin, password+"\n"); err != nil {
			cancel()
			_ = stdin.Close()
			_ = taskcontrol.WaitCommand(ctx, cmd)
			return err
		}
	}
	_ = stdin.Close()

	chunks := make(chan string, 32)
	var readers sync.WaitGroup
	readers.Add(2)
	go streamChunks(stdout, chunks, &readers)
	go streamChunks(stderr, chunks, &readers)
	go func() {
		readers.Wait()
		close(chunks)
	}()
	wait := make(chan error, 1)
	go func() {
		// Wait closes the output pipes, so finish reading them first.
		readers.Wait()
		wait <- taskcontrol.WaitCommand(ctx, cmd)
	}()

	var output strings.Builder
	promptSeen := false
	incorrectPassword := false
	lastPercent := -1
	var processErr error
	var records recordParser
	for chunks != nil || wait != nil {
		select {
		case <-ctx.Done():
			cancel()
			_ = stdin.Close()
			_ = stdout.Close()
			_ = stderr.Close()
			if chunks != nil {
				for range chunks {
				}
			}
			if wait != nil {
				<-wait
			}
			return ctx.Err()
		case chunk, ok := <-chunks:
			if !ok {
				chunks = nil
				continue
			}
			appendLimited(&output, chunk, 8192)
			lower := strings.ToLower(output.String())
			if incorrectPasswordOutput(lower) {
				incorrectPassword = true
			}
			if passwordPromptOutput(lower) {
				promptSeen = true
			}
			for _, record := range records.Feed(chunk) {
				for _, percent := range progressPercents(record) {
					if percent != lastPercent {
						lastPercent = percent
						if report != nil {
							report(Progress{Phase: "extracting", Percent: percent, PercentKnown: true, Message: "Extracting archive — files will appear when complete"})
						}
					}
				}
			}
		case runErr := <-wait:
			wait = nil
			_ = stdin.Close()
			processErr = runErr
		}
	}
	for _, percent := range progressPercents(records.Flush()) {
		if percent != lastPercent {
			lastPercent = percent
			if report != nil {
				report(Progress{Phase: "extracting", Percent: percent, PercentKnown: true, Message: "Extracting archive — files will appear when complete"})
			}
		}
	}
	if processErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if incorrectPassword || incorrectPasswordOutput(strings.ToLower(output.String())) {
			return ErrIncorrectPassword
		}
		if promptSeen && password == "" {
			return ErrPasswordRequired
		}
		return fmt.Errorf("archive tool failed: %w: %s", processErr, sanitizeToolError(output.String()))
	}
	if report != nil && lastPercent != 100 {
		report(Progress{Phase: "extracting", Percent: 100, PercentKnown: true, Message: "Archive extracted"})
	}
	return nil
}

func streamChunks(reader io.Reader, chunks chan<- string, wg *sync.WaitGroup) {
	defer wg.Done()
	buffer := make([]byte, 2048)
	for {
		n, err := reader.Read(buffer)
		if n > 0 {
			chunks <- string(buffer[:n])
		}
		if err != nil {
			return
		}
	}
}

var percentPattern = regexp.MustCompile(`(?:^|[\r\n\s])(\d{1,3})%`)

func progressPercents(text string) []int {
	matches := percentPattern.FindAllStringSubmatch(text, -1)
	values := make([]int, 0, len(matches))
	for _, match := range matches {
		value, err := strconv.Atoi(match[1])
		if err == nil && value >= 0 && value <= 100 {
			values = append(values, value)
		}
	}
	return values
}

type recordParser struct {
	pending strings.Builder
}

func (p *recordParser) Feed(value string) []string {
	records := make([]string, 0)
	for _, current := range []byte(value) {
		if current == '\r' || current == '\n' || current == '\b' {
			if p.pending.Len() > 0 {
				records = append(records, p.pending.String())
				p.pending.Reset()
			}
			continue
		}
		p.pending.WriteByte(current)
	}
	return records
}

func (p *recordParser) Flush() string {
	value := p.pending.String()
	p.pending.Reset()
	return value
}

func passwordPromptOutput(text string) bool {
	return strings.Contains(text, "enter password") || strings.Contains(text, "password (will not be echoed)")
}

func incorrectPasswordOutput(text string) bool {
	return strings.Contains(text, "wrong password") || strings.Contains(text, "incorrect password") || strings.Contains(text, "data error in encrypted file")
}

func appendLimited(builder *strings.Builder, value string, limit int) {
	builder.WriteString(value)
	if builder.Len() <= limit {
		return
	}
	text := builder.String()
	builder.Reset()
	builder.WriteString(text[len(text)-limit:])
}

func sanitizeToolError(output string) string {
	lines := strings.Fields(strings.NewReplacer("\r", "\n", "\x00", "").Replace(output))
	if len(lines) == 0 {
		return "archive tool returned an error"
	}
	message := strings.Join(lines, " ")
	if len(message) > 1000 {
		message = message[len(message)-1000:]
	}
	return message
}

// PublicationRecoveryError identifies extraction data retained because rollback
// could not safely restore every destination. Callers must surface this even
// when the operation was cancelled.
type PublicationRecoveryError struct {
	Directory string
	Err       error
}

func (e *PublicationRecoveryError) Error() string {
	return fmt.Sprintf("extraction rollback incomplete; recovery files retained at %s: %v", e.Directory, e.Err)
}

func (e *PublicationRecoveryError) Unwrap() error { return e.Err }

func (e *PublicationRecoveryError) ClientMessage() clienterror.Message { return clienterror.Recovery }

func publishStage(ctx context.Context, stage, destination string, destinationExists bool, policy ExistingFilePolicy, report ProgressReporter, checks ...func() error) (result ExtractResult, err error) {
	retainStage := false
	defer func() {
		if stage != "" && !retainStage {
			_ = removeStage(stage)
		}
	}()
	if err := taskcontrol.Wait(ctx); err != nil {
		return ExtractResult{}, err
	}
	if report != nil {
		report(Progress{Phase: "validating", Message: "Validating extracted files"})
	}
	count, err := validateStageContext(ctx, stage)
	if err != nil {
		return ExtractResult{}, err
	}
	for _, check := range checks {
		if err := taskcontrol.Wait(ctx); err != nil {
			return ExtractResult{}, err
		}
		if err := check(); err != nil {
			return ExtractResult{}, err
		}
	}
	conflicts, err := preflightContext(ctx, stage, destination, policy)
	if err != nil {
		return ExtractResult{}, err
	}
	if len(conflicts) > 0 {
		return ExtractResult{}, fmt.Errorf("destination conflicts: %s", strings.Join(conflicts, ", "))
	}
	if report != nil {
		report(Progress{Phase: "publishing", ItemsTotal: int64(count), Message: "Publishing extracted files"})
	}
	published := make([]publishedMove, 0)
	done := int64(0)
	skipped := 0
	if destinationExists {
		err = publishNode(ctx, stage, destination, policy, &skipped, &published, &done, int64(count), report)
	} else {
		err = publishAbsent(ctx, stage, destination, count, &published, &done, int64(count), report)
	}
	if err == nil {
		err = ctx.Err()
	}
	if err != nil {
		if rollbackErr := rollbackPublished(published); rollbackErr != nil {
			retainStage = true
			return ExtractResult{}, &PublicationRecoveryError{Directory: stage, Err: errors.Join(err, rollbackErr)}
		}
		return ExtractResult{}, err
	}
	if !destinationExists {
		stage = ""
	}
	return ExtractResult{Extracted: count - skipped, Skipped: skipped}, nil
}

func validateStage(stage string) (int, error) {
	return validateStageContext(context.Background(), stage)
}

func validateStageContext(ctx context.Context, stage string) (int, error) {
	files := 0
	err := filepath.WalkDir(stage, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := taskcontrol.Wait(ctx); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if path == stage {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("archive contains a symbolic link: %s", entry.Name())
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("archive contains an unsupported special file: %s", entry.Name())
		}
		relative, err := filepath.Rel(stage, path)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
			return errors.New("archive entry escapes staging")
		}
		if info.Mode().IsRegular() {
			files++
		}
		// Repair only the extracted entry, before WalkDir descends into it.
		// Missing/unusable modes must not hide the contents from their owner.
		// Never add write bits: zero-mode and read-only entries stay read-only,
		// and valid modes (including executable and special bits) are retained.
		mode := info.Mode() | 0o400
		if info.IsDir() {
			mode |= 0o100
		}
		if mode != info.Mode() {
			if err := os.Chmod(path, mode); err != nil {
				return fmt.Errorf("repair extracted permissions: %w", err)
			}
		}
		return nil
	})
	return files, err
}

// Private staging directories may be read-only or unsearchable, including
// after a failed tool invocation. Make only those directories removable;
// never follow links or touch the destination tree.
func removeStage(stage string) error {
	err := filepath.WalkDir(stage, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		return os.Chmod(path, info.Mode()|0o700)
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return os.RemoveAll(stage)
}

func preflight(source, destination string, policy ExistingFilePolicy) ([]string, error) {
	return preflightContext(context.Background(), source, destination, policy)
}

func preflightContext(ctx context.Context, source, destination string, policy ExistingFilePolicy) ([]string, error) {
	conflicts := make([]string, 0)
	if err := preflightNode(ctx, source, destination, destination, policy, &conflicts); err != nil {
		return nil, err
	}
	sort.Strings(conflicts)
	return conflicts, nil
}

func preflightNode(ctx context.Context, source, destination, root string, policy ExistingFilePolicy, conflicts *[]string) error {
	if err := taskcontrol.Wait(ctx); err != nil {
		return err
	}
	destinationInfo, err := os.Lstat(destination)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	sourceInfo, err := os.Lstat(source)
	if err != nil {
		return err
	}
	regularFileConflict := sourceInfo.Mode().IsRegular() && destinationInfo.Mode().IsRegular()
	if !sourceInfo.IsDir() || !destinationInfo.IsDir() {
		if regularFileConflict && (policy == ExistingFilesSkip || policy == ExistingFilesOverwrite) {
			return nil
		}
		relative, relErr := filepath.Rel(root, destination)
		if relErr != nil || relative == "." {
			relative = filepath.Base(destination)
		}
		*conflicts = append(*conflicts, relative)
		return nil
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := preflightNode(ctx, filepath.Join(source, entry.Name()), filepath.Join(destination, entry.Name()), root, policy, conflicts); err != nil {
			return err
		}
	}
	return nil
}

type publishedMove struct {
	source        string
	destination   string
	directoryMode fs.FileMode
	publishedInfo fs.FileInfo
	originalInfo  fs.FileInfo
}

func publishNode(ctx context.Context, source, destination string, policy ExistingFilePolicy, skipped *int, published *[]publishedMove, done *int64, total int64, report ProgressReporter) error {
	if err := taskcontrol.Wait(ctx); err != nil {
		return err
	}
	destinationInfo, err := os.Lstat(destination)
	if errors.Is(err, fs.ErrNotExist) {
		files, countErr := countRegularFilesContext(ctx, source)
		if countErr != nil {
			return countErr
		}
		return publishAbsent(ctx, source, destination, files, published, done, total, report)
	}
	if err != nil {
		return err
	}
	sourceInfo, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if sourceInfo.Mode().IsRegular() && destinationInfo.Mode().IsRegular() {
		if policy == ExistingFilesSkip {
			*skipped += 1
			*done += 1
			if report != nil {
				report(Progress{Phase: "publishing", ItemsDone: *done, ItemsTotal: total, Message: "Publishing extracted files"})
			}
			return nil
		}
		if policy == ExistingFilesOverwrite {
			// Replacing contents must not change existing destination permissions.
			if err := os.Chmod(source, destinationInfo.Mode()); err != nil {
				return err
			}
			sourceInfo, err = os.Lstat(source)
			if err != nil {
				return err
			}
			// Keep the displaced original in the private stage until the entire
			// publication succeeds. Never fall back to a destructive rename.
			if err := exchangeFiles(source, destination); err != nil {
				return err
			}
			move := publishedMove{source: source, destination: destination, publishedInfo: sourceInfo, originalInfo: destinationInfo}
			originalInfo, statErr := os.Lstat(source)
			if statErr == nil {
				move.originalInfo = originalInfo
			}
			*published = append(*published, move)
			if statErr != nil {
				return statErr
			}
			if !sameFileState(originalInfo, destinationInfo) {
				return filesystem.ErrConflict
			}
			*done += 1
			if report != nil {
				report(Progress{Phase: "publishing", ItemsDone: *done, ItemsTotal: total, Message: "Publishing extracted files"})
			}
			return nil
		}
	}
	if !sourceInfo.IsDir() || !destinationInfo.IsDir() {
		return filesystem.ErrConflict
	}
	// This staging directory is being merged into an existing directory, so
	// its mode will not be published. Renaming its children requires owner write.
	if err := os.Chmod(source, sourceInfo.Mode()|0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := publishNode(ctx, filepath.Join(source, entry.Name()), filepath.Join(destination, entry.Name()), policy, skipped, published, done, total, report); err != nil {
			return err
		}
	}
	return nil
}

func publishAbsent(ctx context.Context, source, destination string, files int, published *[]publishedMove, done *int64, total int64, report ProgressReporter) error {
	if err := taskcontrol.Wait(ctx); err != nil {
		return err
	}
	err := filesystem.RenameNoReplace(source, destination)
	if errors.Is(err, filesystem.ErrRenameNoReplaceUnsupported) {
		info, statErr := os.Lstat(source)
		if statErr != nil {
			return statErr
		}
		if !info.IsDir() {
			return err
		}
		return publishDirectoryAbsent(ctx, source, destination, info, published, done, total, report)
	}
	if err != nil {
		return err
	}
	*published = append(*published, publishedMove{source: source, destination: destination})
	*done += int64(files)
	if report != nil {
		report(Progress{Phase: "publishing", ItemsDone: *done, ItemsTotal: total, Message: "Publishing extracted files"})
	}
	return nil
}

func publishDirectoryAbsent(ctx context.Context, source, destination string, info fs.FileInfo, published *[]publishedMove, done *int64, total int64, report ProgressReporter) error {
	if err := os.Mkdir(destination, info.Mode().Perm()|0o700); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return filesystem.ErrConflict
		}
		return err
	}
	*published = append(*published, publishedMove{source: source, destination: destination, directoryMode: info.Mode()})
	if err := os.Chmod(source, info.Mode()|0o700); err != nil {
		return err
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := taskcontrol.Wait(ctx); err != nil {
			return err
		}
		childSource := filepath.Join(source, entry.Name())
		childDestination := filepath.Join(destination, entry.Name())
		files, err := countRegularFilesContext(ctx, childSource)
		if err != nil {
			return err
		}
		if err := publishAbsent(ctx, childSource, childDestination, files, published, done, total, report); err != nil {
			return err
		}
	}
	if err := os.Chmod(destination, info.Mode()); err != nil {
		return err
	}
	if err := os.Chtimes(destination, info.ModTime(), info.ModTime()); err != nil {
		return err
	}
	return os.Remove(source)
}

func countRegularFiles(root string) (int, error) {
	return countRegularFilesContext(context.Background(), root)
}

func countRegularFilesContext(ctx context.Context, root string) (int, error) {
	if err := taskcontrol.Wait(ctx); err != nil {
		return 0, err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return 0, err
	}
	if !info.IsDir() {
		if info.Mode().IsRegular() {
			return 1, nil
		}
		return 0, nil
	}
	count := 0
	err = filepath.WalkDir(root, func(_ string, entry fs.DirEntry, walkErr error) error {
		if err := taskcontrol.Wait(ctx); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			count++
		}
		return nil
	})
	return count, err
}

func rollbackPublished(published []publishedMove) error {
	var result error
	for index := len(published) - 1; index >= 0; index-- {
		move := published[index]
		if move.originalInfo != nil {
			result = errors.Join(result, restoreReplacement(move))
			continue
		}
		if move.directoryMode != 0 {
			if err := os.MkdirAll(move.source, move.directoryMode.Perm()); err != nil {
				result = errors.Join(result, err)
			}
			if err := os.Remove(move.destination); err != nil && !errors.Is(err, fs.ErrNotExist) {
				result = errors.Join(result, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(move.source), 0o700); err != nil {
			result = errors.Join(result, err)
			continue
		}
		if err := filesystem.RenameNoReplace(move.destination, move.source); err != nil && !errors.Is(err, fs.ErrNotExist) {
			result = errors.Join(result, err)
		}
	}
	return result
}

func exchangeFiles(source, destination string) error {
	return unix.Renameat2(unix.AT_FDCWD, source, unix.AT_FDCWD, destination, unix.RENAME_EXCHANGE)
}

func sameFileState(a, b fs.FileInfo) bool {
	return os.SameFile(a, b) && a.Mode() == b.Mode() && a.Size() == b.Size() && a.ModTime().Equal(b.ModTime())
}

func restoreReplacement(move publishedMove) error {
	original, err := os.Lstat(move.source)
	if err != nil {
		return err
	}
	if !sameFileState(original, move.originalInfo) {
		return fmt.Errorf("backup changed: %s", move.source)
	}
	current, err := os.Lstat(move.destination)
	if errors.Is(err, fs.ErrNotExist) {
		return filesystem.RenameNoReplace(move.source, move.destination)
	}
	if err != nil {
		return err
	}
	if !sameFileState(current, move.publishedInfo) {
		return fmt.Errorf("destination changed during extraction: %s", move.destination)
	}
	if err := exchangeFiles(move.source, move.destination); err != nil {
		return err
	}
	// A concurrent replacement between the check and exchange must survive in
	// staging, even though the original has now been restored successfully.
	displaced, err := os.Lstat(move.source)
	if err != nil {
		return err
	}
	if !sameFileState(displaced, move.publishedInfo) {
		return fmt.Errorf("destination changed during rollback: %s", move.destination)
	}
	return nil
}

func scanRecords(reader io.Reader, emit func(string)) error {
	scanner := bufio.NewScanner(reader)
	scanner.Split(splitArchiveRecords)
	for scanner.Scan() {
		emit(scanner.Text())
	}
	return scanner.Err()
}

func splitArchiveRecords(data []byte, atEOF bool) (advance int, token []byte, err error) {
	for index, value := range data {
		if value == '\r' || value == '\n' {
			return index + 1, data[:index], nil
		}
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}
