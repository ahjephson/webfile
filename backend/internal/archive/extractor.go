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

	"webfile/internal/filesystem"
)

var (
	ErrPasswordRequired  = errors.New("archive password is required")
	ErrIncorrectPassword = errors.New("incorrect archive password")
)

type Config struct {
	Executable         string
	ExtractArgs        []string
	InspectArgs        []string
	InspectPasswordArg string
}

type Progress struct {
	Phase      string
	Percent    int
	ItemsDone  int64
	ItemsTotal int64
	Message    string
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
	if !e.Enabled() {
		return 0, errors.New("archive extraction is not configured")
	}
	_, archiveAbs, err := e.resolver.ResolveExisting(archivePath)
	if err != nil {
		return 0, err
	}
	_, destinationAbs, err := e.resolver.ResolveExisting(destinationPath)
	destinationExists := err == nil
	if errors.Is(err, fs.ErrNotExist) {
		_, destinationAbs, err = e.resolver.ResolveForCreate(destinationPath)
	}
	if err != nil {
		return 0, err
	}
	if destinationExists {
		info, statErr := os.Stat(destinationAbs)
		if statErr != nil {
			return 0, statErr
		}
		if !info.IsDir() {
			return 0, errors.New("extraction destination is not a directory")
		}
	}

	passwordValue := ""
	passwordRequired, err := e.PasswordRequired(ctx, archiveAbs)
	if err != nil {
		return 0, err
	}
	if passwordRequired {
		if password == nil {
			return 0, ErrPasswordRequired
		}
		passwordValue, err = password(ctx, false)
		if err != nil {
			return 0, err
		}
	}
	incorrect := false
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		stageParent := filepath.Dir(destinationAbs)
		if destinationExists {
			stageParent = destinationAbs
		}
		stage, err := os.MkdirTemp(stageParent, ".webfile-extract-")
		if err != nil {
			return 0, err
		}
		runErr := e.runTool(ctx, archiveAbs, stage, passwordValue, report)
		if errors.Is(runErr, ErrPasswordRequired) {
			if cleanupErr := os.RemoveAll(stage); cleanupErr != nil {
				return 0, errors.Join(runErr, fmt.Errorf("clean password attempt: %w", cleanupErr))
			}
			if password == nil {
				return 0, ErrPasswordRequired
			}
			passwordValue, err = password(ctx, false)
			if err != nil {
				return 0, err
			}
			continue
		}
		if errors.Is(runErr, ErrIncorrectPassword) {
			if cleanupErr := os.RemoveAll(stage); cleanupErr != nil {
				return 0, errors.Join(runErr, fmt.Errorf("clean password attempt: %w", cleanupErr))
			}
			incorrect = true
			if password == nil {
				return 0, ErrIncorrectPassword
			}
			passwordValue, err = password(ctx, incorrect)
			if err != nil {
				return 0, err
			}
			continue
		}
		if runErr != nil {
			_ = os.RemoveAll(stage)
			return 0, runErr
		}
		return publishStage(ctx, stage, destinationAbs, destinationExists, report)
	}
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
	output, err := cmd.CombinedOutput()
	text := strings.ToLower(string(output))
	if incorrectPasswordOutput(text) || strings.Contains(text, "encrypted archive") {
		return true, nil
	}
	for _, line := range strings.Split(string(output), "\n") {
		fields := strings.SplitN(line, "=", 2)
		if len(fields) == 2 && strings.EqualFold(strings.TrimSpace(fields[0]), "Encrypted") && strings.TrimSpace(fields[1]) == "+" {
			return true, nil
		}
	}
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return false, ctxErr
		}
		return false, fmt.Errorf("archive inspection failed: %w: %s", err, sanitizeToolError(string(output)))
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
	if err := cmd.Start(); err != nil {
		return err
	}
	if password != "" {
		if _, err := io.WriteString(stdin, password+"\n"); err != nil {
			cancel()
			_ = stdin.Close()
			_ = cmd.Wait()
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
	go func() { wait <- cmd.Wait() }()

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
							report(Progress{Phase: "extracting", Percent: percent, Message: "Extracting archive"})
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
				report(Progress{Phase: "extracting", Percent: percent, Message: "Extracting archive"})
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
		report(Progress{Phase: "extracting", Percent: 100, Message: "Archive extracted"})
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
		if current == '\r' || current == '\n' {
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

func publishStage(ctx context.Context, stage, destination string, destinationExists bool, report ProgressReporter) (count int, err error) {
	defer func() {
		if stage != "" {
			_ = os.RemoveAll(stage)
		}
	}()
	if report != nil {
		report(Progress{Phase: "validating", Message: "Validating extracted files"})
	}
	count, err = validateStage(stage)
	if err != nil {
		return 0, err
	}
	conflicts, err := preflight(stage, destination)
	if err != nil {
		return 0, err
	}
	if len(conflicts) > 0 {
		return 0, fmt.Errorf("destination conflicts: %s", strings.Join(conflicts, ", "))
	}
	if report != nil {
		report(Progress{Phase: "publishing", ItemsTotal: int64(count), Message: "Publishing extracted files"})
	}
	published := make([]publishedMove, 0)
	done := int64(0)
	if destinationExists {
		err = publishNode(ctx, stage, destination, &published, &done, int64(count), report)
	} else {
		err = publishAbsent(ctx, stage, destination, count, &published, &done, int64(count), report)
		if err == nil {
			stage = ""
		}
	}
	if err != nil {
		if rollbackErr := rollbackPublished(published); rollbackErr != nil {
			return 0, errors.Join(err, fmt.Errorf("rollback published files: %w", rollbackErr))
		}
		return 0, err
	}
	return count, nil
}

func validateStage(stage string) (int, error) {
	files := 0
	err := filepath.WalkDir(stage, func(path string, entry fs.DirEntry, walkErr error) error {
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
		return nil
	})
	return files, err
}

func preflight(source, destination string) ([]string, error) {
	conflicts := make([]string, 0)
	if err := preflightNode(source, destination, destination, &conflicts); err != nil {
		return nil, err
	}
	sort.Strings(conflicts)
	return conflicts, nil
}

func preflightNode(source, destination, root string, conflicts *[]string) error {
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
	if !sourceInfo.IsDir() || !destinationInfo.IsDir() {
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
		if err := preflightNode(filepath.Join(source, entry.Name()), filepath.Join(destination, entry.Name()), root, conflicts); err != nil {
			return err
		}
	}
	return nil
}

type publishedMove struct {
	source        string
	destination   string
	directoryMode fs.FileMode
}

func publishNode(ctx context.Context, source, destination string, published *[]publishedMove, done *int64, total int64, report ProgressReporter) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	destinationInfo, err := os.Lstat(destination)
	if errors.Is(err, fs.ErrNotExist) {
		files, countErr := countRegularFiles(source)
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
	if !sourceInfo.IsDir() || !destinationInfo.IsDir() {
		return filesystem.ErrConflict
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := publishNode(ctx, filepath.Join(source, entry.Name()), filepath.Join(destination, entry.Name()), published, done, total, report); err != nil {
			return err
		}
	}
	return nil
}

func publishAbsent(ctx context.Context, source, destination string, files int, published *[]publishedMove, done *int64, total int64, report ProgressReporter) error {
	if err := ctx.Err(); err != nil {
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
	if err := os.Mkdir(destination, info.Mode().Perm()); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return filesystem.ErrConflict
		}
		return err
	}
	*published = append(*published, publishedMove{source: source, destination: destination, directoryMode: info.Mode()})
	entries, err := os.ReadDir(source)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		childSource := filepath.Join(source, entry.Name())
		childDestination := filepath.Join(destination, entry.Name())
		files, err := countRegularFiles(childSource)
		if err != nil {
			return err
		}
		if err := publishAbsent(ctx, childSource, childDestination, files, published, done, total, report); err != nil {
			return err
		}
	}
	if err := os.Chmod(destination, info.Mode().Perm()); err != nil {
		return err
	}
	if err := os.Chtimes(destination, info.ModTime(), info.ModTime()); err != nil {
		return err
	}
	return os.Remove(source)
}

func countRegularFiles(root string) (int, error) {
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
