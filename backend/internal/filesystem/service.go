package filesystem

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"webfile/internal/clienterror"
	"webfile/internal/thumbnailcache"
	"webfile/internal/thumbnailsettings"
)

var (
	ErrTooLarge     = clienterror.New("file is too large for the editor", clienterror.TooLarge)
	ErrConflict     = clienterror.New("file changed or already exists", clienterror.Exists)
	ErrNotDirectory = clienterror.New("path is not a directory", clienterror.NotDirectory)
)

type Entry struct {
	Name             string     `json:"name"`
	Path             string     `json:"path"`
	IsDir            bool       `json:"is_dir"`
	Size             int64      `json:"size"`
	SizeStatus       string     `json:"size_status,omitempty"`
	SizeRefreshing   bool       `json:"size_refreshing,omitempty"`
	SizeCalculatedAt *time.Time `json:"size_calculated_at,omitempty"`
	Modified         time.Time  `json:"modified"`
	Mode             string     `json:"mode"`
	UID              uint32     `json:"uid,omitempty"`
	GID              uint32     `json:"gid,omitempty"`
	CanNavigate      *bool      `json:"can_navigate,omitempty"`
	CanRead          *bool      `json:"can_read,omitempty"`
}

type FileContent struct {
	Path     string    `json:"path"`
	Content  string    `json:"content"`
	Modified time.Time `json:"modified"`
	ETag     string    `json:"etag"`
}

type FileType struct {
	Path        string `json:"path"`
	ContentType string `json:"content_type"`
}

type Service struct {
	searchSlots         chan struct{}
	resolver            *Resolver
	editorMaxBytes      int64
	thumbnailCache      thumbnailcache.Cache
	thumbnailSlots      *thumbnailLimiter
	thumbnailSettingsMu sync.Mutex
	thumbnailSettings   thumbnailsettings.Settings
	thumbnailFlightMu   sync.Mutex
	thumbnailFlights    map[string]*thumbnailFlight
	videoThumbnails     videoThumbnailGenerator
	videoQueue          *videoThumbnailQueue
	folderSizeMu        sync.Mutex
	folderSizeCache     map[string]folderSizeCacheEntry
	folderSizePending   map[string]*folderSizeJob
	folderSizeSlots     chan struct{}
	folderSizeContext   context.Context
	folderSizeCancel    context.CancelFunc
	folderSizeWait      sync.WaitGroup
	folderSizeClosed    bool
}

func NewService(resolver *Resolver, editorMaxBytes int64) *Service {
	return NewServiceWithThumbnailOptions(resolver, editorMaxBytes, ThumbnailOptions{})
}

func NewServiceWithThumbnailOptions(resolver *Resolver, editorMaxBytes int64, options ThumbnailOptions) *Service {
	options.applyDefaults()
	service := &Service{
		searchSlots:       make(chan struct{}, 1),
		resolver:          resolver,
		editorMaxBytes:    editorMaxBytes,
		thumbnailCache:    options.Cache,
		thumbnailSlots:    newThumbnailLimiter(options.ImageWorkers),
		thumbnailSettings: thumbnailsettings.Settings{ImagesEnabled: !options.ImagesDisabled, VideosEnabled: !options.VideosDisabled, ImageWorkers: options.ImageWorkers, VideoWorkers: options.VideoWorkers, VideoFFmpegThreads: options.VideoFFmpegThreads, VideoTimeoutSeconds: int(options.VideoTimeout.Seconds()), MaxQueued: options.MaxQueued},
		thumbnailFlights:  make(map[string]*thumbnailFlight),
		videoThumbnails:   videoThumbnailGenerator{ffmpeg: "ffmpeg", ffprobe: "ffprobe", threads: options.VideoFFmpegThreads},
		folderSizeCache:   make(map[string]folderSizeCacheEntry),
		folderSizePending: make(map[string]*folderSizeJob),
		folderSizeSlots:   make(chan struct{}, 2),
	}
	service.folderSizeContext, service.folderSizeCancel = context.WithCancel(context.Background())
	service.videoQueue = newVideoThumbnailQueue(service, options)
	if options.ImagesDisabled {
		service.thumbnailSlots.setLimit(0)
	}
	if options.VideosDisabled {
		service.videoQueue.configure(0, options.MaxQueued, options.VideoTimeout)
	}
	return service
}

func (s *Service) Close() {
	s.folderSizeMu.Lock()
	s.folderSizeClosed = true
	if s.folderSizeCancel != nil {
		s.folderSizeCancel()
	}
	for _, job := range s.folderSizePending {
		job.cancel()
	}
	s.folderSizeMu.Unlock()
	s.folderSizeWait.Wait()
	s.videoQueue.close()
}

func (s *Service) Resolver() *Resolver { return s.resolver }

func (s *Service) List(ctx context.Context, virtualPath string) ([]Entry, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	virtualPath = CleanVirtual(virtualPath)
	if virtualPath == "/" {
		roots := s.resolver.Roots()
		entries := make([]Entry, 0, len(roots))
		for _, root := range roots {
			st, err := s.resolver.disk.Stat(root.Path)
			if err != nil {
				continue
			}
			entry := toEntry("/", root.Name, st)
			canNavigate := canNavigateDirectory(root.Path)
			entry.CanNavigate = &canNavigate
			s.applyCachedFolderSize(&entry, root.Path)
			entries = append(entries, entry)
		}
		return entries, nil
	}

	root, abs, err := s.resolver.ResolveExisting(virtualPath)
	if err != nil {
		return nil, err
	}
	st, err := s.resolver.disk.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !st.IsDir() {
		return nil, ErrNotDirectory
	}

	dirEntries, err := s.resolver.disk.ReadDir(abs)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(dirEntries))
	excludedNames := makeNameSet(root.ExcludedNames)
	collationKeys := make(map[string]string, len(dirEntries))
	for _, de := range dirEntries {
		if strings.HasPrefix(de.Name(), ".webfile-extract-") {
			continue
		}
		if _, excluded := excludedNames[de.Name()]; excluded {
			continue
		}
		entryPath := filepath.Join(abs, de.Name())
		if isExcludedPath(root, entryPath) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		info, err := de.Info()
		if err != nil {
			continue
		}
		entry := toEntry(virtualPath, de.Name(), info)
		if entry.IsDir {
			canNavigate := canNavigateDirectory(entryPath)
			entry.CanNavigate = &canNavigate
			s.applyCachedFolderSize(&entry, entryPath)
		} else {
			canRead := canReadFile(entryPath)
			entry.CanRead = &canRead
		}
		out = append(out, entry)
		collationKeys[entry.Name] = hostCollationKey(entry.Name)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		left := collationKeys[out[i].Name]
		right := collationKeys[out[j].Name]
		if left == right {
			return out[i].Name < out[j].Name
		}
		return left < right
	})
	return out, nil
}

func makeNameSet(names []string) map[string]struct{} {
	set := make(map[string]struct{}, len(names))
	for _, name := range names {
		set[name] = struct{}{}
	}
	return set
}

func (s *Service) ReadText(ctx context.Context, virtualPath string) (FileContent, error) {
	if err := ctx.Err(); err != nil {
		return FileContent{}, err
	}
	file, st, err := s.OpenDownload(ctx, virtualPath)
	if err != nil {
		return FileContent{}, err
	}
	defer file.Close()
	if st.Size() > s.editorMaxBytes {
		return FileContent{}, ErrTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(file, s.editorMaxBytes+1))
	if err != nil {
		return FileContent{}, err
	}
	if int64(len(data)) > s.editorMaxBytes {
		return FileContent{}, ErrTooLarge
	}
	return FileContent{
		Path:     CleanVirtual(virtualPath),
		Content:  string(data),
		Modified: st.ModTime(),
		ETag:     etag(st),
	}, nil
}

func (s *Service) DetectContentType(ctx context.Context, virtualPath string) (FileType, error) {
	if err := ctx.Err(); err != nil {
		return FileType{}, err
	}
	file, _, err := s.OpenDownload(ctx, virtualPath)
	if err != nil {
		return FileType{}, err
	}
	defer file.Close()

	sample := make([]byte, 512)
	n, err := file.Read(sample)
	if err != nil && !errors.Is(err, io.EOF) {
		return FileType{}, err
	}
	sample = sample[:n]
	contentType := http.DetectContentType(sample)
	if looksLikeText(sample) && contentType == "application/octet-stream" {
		contentType = "text/plain; charset=utf-8"
	}
	if extensionType := mime.TypeByExtension(filepath.Ext(virtualPath)); strings.HasPrefix(extensionType, "video/") && contentType == "application/octet-stream" {
		contentType = extensionType
	}
	// Ogg and MP4 sniffing identifies the container rather than whether its
	// tracks are audio. Use an audio extension for these ambiguous containers
	// and formats without a sniff signature, but never override active/text content.
	if extensionType := mime.TypeByExtension(filepath.Ext(virtualPath)); strings.HasPrefix(extensionType, "audio/") &&
		(contentType == "application/octet-stream" || contentType == "application/ogg" || contentType == "video/mp4") {
		contentType = extensionType
	}
	return FileType{Path: CleanVirtual(virtualPath), ContentType: contentType}, nil
}

func looksLikeText(data []byte) bool {
	if len(data) == 0 {
		return true
	}
	if !utf8.Valid(data) {
		return false
	}
	for _, value := range data {
		if value == 0 || value < 0x09 || value > 0x0d && value < 0x20 {
			return false
		}
	}
	return true
}

func (s *Service) WriteText(ctx context.Context, virtualPath string, content []byte, ifMatch string, createOnly bool) (FileContent, error) {
	if int64(len(content)) > s.editorMaxBytes {
		return FileContent{}, ErrTooLarge
	}
	if err := ctx.Err(); err != nil {
		return FileContent{}, err
	}
	var abs string
	var err error
	if createOnly {
		_, abs, err = s.resolver.ResolveForCreate(virtualPath)
	} else {
		_, abs, err = s.resolver.ResolveExisting(virtualPath)
	}
	if err != nil {
		return FileContent{}, err
	}
	release, err := lockEditorDirectory(ctx, s.resolver.disk, abs)
	if err != nil {
		return FileContent{}, err
	}
	defer release()
	var savedInfo os.FileInfo

	if createOnly {
		f, err := s.resolver.disk.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			if errors.Is(err, fs.ErrExist) {
				return FileContent{}, ErrConflict
			}
			return FileContent{}, err
		}
		if _, err := f.Write(content); err != nil {
			_ = f.Close()
			_ = s.resolver.disk.Remove(abs)
			return FileContent{}, err
		}
		if err := f.Close(); err != nil {
			return FileContent{}, err
		}
	} else {
		st, err := s.resolver.disk.Stat(abs)
		if err != nil {
			return FileContent{}, err
		}
		if ifMatch != "" && ifMatch != etag(st) {
			return FileContent{}, ErrConflict
		}
		if hasMultipleLinks(st) {
			file, err := s.resolver.disk.OpenFile(abs, os.O_WRONLY, 0)
			if err != nil {
				return FileContent{}, err
			}
			defer file.Close()
			// Hard-link aliases can have different containing directories.
			// Lock their shared inode too, then recheck the ETag under that lock.
			if err := lockEditorFile(ctx, file); err != nil {
				return FileContent{}, err
			}
			st, err = file.Stat()
			if err != nil {
				return FileContent{}, err
			}
			if ifMatch != "" && ifMatch != etag(st) {
				return FileContent{}, ErrConflict
			}
			if err := writeLinkedFile(file, content); err != nil {
				return FileContent{}, err
			}
			savedInfo, err = file.Stat()
			if err != nil {
				return FileContent{}, err
			}
			goto saved
		}
		tmp, err := s.resolver.disk.CreateTemp(filepath.Dir(abs), ".webfile-save-*")
		if err != nil {
			return FileContent{}, err
		}
		tmpName := tmp.Name()
		defer s.resolver.disk.Remove(tmpName)
		if err := copyReplacementMetadata(s.resolver.disk, abs, tmp, st); err != nil {
			_ = tmp.Close()
			return FileContent{}, err
		}
		if _, err := tmp.Write(content); err != nil {
			_ = tmp.Close()
			return FileContent{}, err
		}
		if err := tmp.Sync(); err != nil {
			_ = tmp.Close()
			return FileContent{}, err
		}
		if err := tmp.Close(); err != nil {
			return FileContent{}, err
		}
		if err := s.resolver.disk.Rename(tmpName, abs); err != nil {
			return FileContent{}, err
		}
	}

saved:
	if savedInfo == nil {
		savedInfo, err = s.resolver.disk.Stat(abs)
		if err != nil {
			return FileContent{}, err
		}
	}
	s.InvalidateFolderSizes(virtualPath)
	return FileContent{
		Path:     CleanVirtual(virtualPath),
		Content:  string(content),
		Modified: savedInfo.ModTime(),
		ETag:     etag(savedInfo),
	}, nil
}

func (s *Service) Mkdir(ctx context.Context, virtualPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	_, abs, err := s.resolver.ResolveForCreate(virtualPath)
	if err != nil {
		return err
	}
	if err := s.resolver.disk.Mkdir(abs, 0o755); err != nil {
		return err
	}
	s.InvalidateFolderSizes(virtualPath)
	return nil
}

func (s *Service) Rename(ctx context.Context, from, to string) error {
	if IsConfiguredRootPath(from) {
		return errors.New("configured roots cannot be renamed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	fromRoot, fromAbs, err := s.resolver.ResolveEntry(from)
	if err != nil {
		return err
	}
	toRoot, toAbs, err := s.resolver.ResolveForCreate(to)
	if err != nil {
		return err
	}
	if fromRoot.Name != toRoot.Name {
		return errors.New("rename cannot cross configured roots; use the move operation")
	}
	if err := fromRoot.validateMoveSource(ctx, fromAbs); err != nil {
		return err
	}
	from = CleanVirtual(from)
	s.videoQueue.cancelPath(ctx, from)
	if err := ctx.Err(); err != nil {
		return err
	}
	tickets := s.captureThumbnailChanges(ctx, []string{from})
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := s.resolver.disk.Lstat(fromAbs)
	if err != nil {
		return err
	}
	if err := renameNoReplace(s.resolver.disk, fromAbs, toAbs); err != nil {
		return err
	}
	s.InvalidateFolderSizes(from, to)
	if len(tickets) == 1 {
		change := thumbnailcache.LocationChange{Ticket: tickets[0]}
		if info.IsDir() {
			if destination, ok := s.thumbnailLocation(to); ok {
				change.Destination = &destination
			}
		}
		s.applyThumbnailChanges([]thumbnailcache.LocationChange{change})
	}
	return nil
}

func (s *Service) Delete(ctx context.Context, virtualPath string) error {
	return s.DeleteWithProgress(ctx, virtualPath, nil)
}

func (s *Service) OpenDownload(ctx context.Context, virtualPath string) (*os.File, os.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	f, err := s.resolver.OpenExisting(virtualPath)
	if err != nil {
		return nil, nil, err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	if !st.Mode().IsRegular() {
		_ = f.Close()
		return nil, nil, errors.New("only regular files can be downloaded")
	}
	return f, st, nil
}

func (s *Service) Upload(ctx context.Context, virtualDir, filename string, src io.Reader) (Entry, error) {
	if err := ctx.Err(); err != nil {
		return Entry{}, err
	}
	virtualPath := VirtualJoin(virtualDir, filepath.Base(filename))
	_, abs, err := s.resolver.ResolveForCreate(virtualPath)
	if err != nil {
		return Entry{}, err
	}
	if _, err := s.resolver.disk.Lstat(abs); err == nil {
		return Entry{}, ErrConflict
	} else if !errors.Is(err, fs.ErrNotExist) {
		return Entry{}, err
	}

	tmp, err := s.resolver.disk.CreateTemp(filepath.Dir(abs), ".webfile-upload-*")
	if err != nil {
		return Entry{}, err
	}
	tmpName := tmp.Name()
	defer s.resolver.disk.Remove(tmpName)
	if _, err := io.Copy(tmp, src); err != nil {
		_ = tmp.Close()
		return Entry{}, err
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return Entry{}, err
	}
	if err := tmp.Close(); err != nil {
		return Entry{}, err
	}
	if err := s.resolver.disk.Link(tmpName, abs); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return Entry{}, ErrConflict
		}
		return Entry{}, err
	}
	s.InvalidateFolderSizes(virtualPath)
	st, err := s.resolver.disk.Stat(abs)
	if err != nil {
		return Entry{}, err
	}
	return toEntry(CleanVirtual(virtualDir), filepath.Base(filename), st), nil
}

func toEntry(parentVirtual, name string, st os.FileInfo) Entry {
	entry := Entry{
		Name:     name,
		Path:     VirtualJoin(parentVirtual, name),
		IsDir:    st.IsDir(),
		Size:     st.Size(),
		Modified: st.ModTime(),
		Mode:     st.Mode().String(),
	}
	if stat, ok := st.Sys().(*syscall.Stat_t); ok {
		entry.UID = stat.Uid
		entry.GID = stat.Gid
	}
	return entry
}

func etag(st os.FileInfo) string {
	return `"` + strconv.FormatInt(st.Size(), 16) + `-` + strconv.FormatInt(st.ModTime().UnixNano(), 16) + `"`
}

func FormatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
