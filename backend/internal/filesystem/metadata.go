package filesystem

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type Details struct {
	Path             string     `json:"path"`
	Name             string     `json:"name"`
	IsDir            bool       `json:"is_dir"`
	IsSymlink        bool       `json:"is_symlink"`
	Size             int64      `json:"size"`
	Modified         time.Time  `json:"modified"`
	Mode             string     `json:"mode"`
	ModeOctal        string     `json:"mode_octal"`
	UID              uint32     `json:"uid"`
	GID              uint32     `json:"gid"`
	Owner            string     `json:"owner"`
	Group            string     `json:"group"`
	LinkTarget       string     `json:"link_target,omitempty"`
	SizeStatus       string     `json:"size_status,omitempty"`
	SizeError        string     `json:"size_error,omitempty"`
	SizeIncomplete   bool       `json:"size_incomplete,omitempty"`
	SizeRefreshing   bool       `json:"size_refreshing,omitempty"`
	SizeCalculatedAt *time.Time `json:"size_calculated_at,omitempty"`
}

const folderSizeCacheTTL = 5 * time.Minute
const folderSizeLeaseTTL = 3 * time.Second
const maxFolderSizeJobs = 32

var errFolderSizeIncomplete = errors.New("some items could not be accessed and were excluded from the total")

type folderSizeJob struct {
	ctx    context.Context
	cancel context.CancelFunc
	leases map[string]time.Time
	timer  *time.Timer
}

type folderSizeCacheEntry struct {
	size              int64
	calculatedAt      time.Time
	directoryModified time.Time
	err               string
	incomplete        bool
	dirty             bool
}

func (s *Service) applyCachedFolderSize(entry *Entry, abs string) {
	s.folderSizeMu.Lock()
	_, ok := s.folderSizeCache[abs]
	s.folderSizeMu.Unlock()
	if !ok {
		return
	} // Browsing must never start scans of previously unscanned folders.
	entry.Size, entry.SizeStatus, _, entry.SizeRefreshing = s.folderSize(context.Background(), abs, false, "listing", entry.Modified)
	s.folderSizeMu.Lock()
	if cached, exists := s.folderSizeCache[abs]; exists {
		entry.SizeCalculatedAt = &cached.calculatedAt
		entry.SizeIncomplete = cached.incomplete
	}
	s.folderSizeMu.Unlock()
}

func (s *Service) Details(ctx context.Context, virtualPath string) (Details, error) {
	if err := ctx.Err(); err != nil {
		return Details{}, err
	}
	var abs string
	var err error
	if IsConfiguredRootPath(virtualPath) {
		_, abs, err = s.resolver.ResolveExisting(virtualPath)
	} else {
		_, abs, err = s.resolver.ResolveEntry(virtualPath)
	}
	if err != nil {
		return Details{}, err
	}
	st, err := s.resolver.disk.Lstat(abs)
	if err != nil {
		return Details{}, err
	}
	d := Details{
		Path:      CleanVirtual(virtualPath),
		Name:      st.Name(),
		IsDir:     st.IsDir(),
		IsSymlink: st.Mode()&os.ModeSymlink != 0,
		Size:      st.Size(),
		Modified:  st.ModTime(),
		Mode:      st.Mode().String(),
		ModeOctal: fmt.Sprintf("%04o", unixPermissionBits(st.Mode())),
	}
	if stat, ok := st.Sys().(*syscall.Stat_t); ok {
		d.UID, d.GID = stat.Uid, stat.Gid
		d.Owner = usernameForID(stat.Uid)
		d.Group = groupnameForID(stat.Gid)
	}
	if d.IsSymlink {
		d.LinkTarget, _ = s.resolver.disk.Readlink(abs)
	}
	return d, nil
}

func (s *Service) DetailsWithFolderSize(ctx context.Context, virtualPath string, refresh bool) (Details, error) {
	return s.DetailsWithFolderSizeLease(ctx, virtualPath, refresh, "")
}

func (s *Service) DetailsWithFolderSizeLease(ctx context.Context, virtualPath string, refresh bool, lease string) (Details, error) {
	details, err := s.Details(ctx, virtualPath)
	if err != nil || !details.IsDir || details.IsSymlink {
		if err == nil {
			details.SizeStatus = "ready"
		}
		return details, err
	}
	_, abs, err := s.resolver.ResolveExisting(virtualPath)
	if err != nil {
		return Details{}, err
	}
	size, status, sizeErr, refreshing := s.folderSize(ctx, abs, refresh, lease, details.Modified)
	details.Size = size
	details.SizeStatus = status
	details.SizeError = sizeErr
	details.SizeRefreshing = refreshing
	s.folderSizeMu.Lock()
	if entry, ok := s.folderSizeCache[abs]; ok && status == "ready" {
		details.SizeCalculatedAt = &entry.calculatedAt
		details.SizeIncomplete = entry.incomplete
	}
	s.folderSizeMu.Unlock()
	return details, nil
}

func (s *Service) folderSize(ctx context.Context, abs string, refresh bool, lease string, modified time.Time) (int64, string, string, bool) {
	now := time.Now()
	s.folderSizeMu.Lock()
	if s.folderSizeClosed {
		s.folderSizeMu.Unlock()
		return 0, "error", "folder-size service is closed", false
	}
	entry, cached := s.folderSizeCache[abs]
	if cached && (entry.dirty || now.Sub(entry.calculatedAt) >= folderSizeCacheTTL || !entry.directoryModified.IsZero() && !entry.directoryModified.Equal(modified)) {
		cached = false
	}
	job := s.folderSizePending[abs]
	if job == nil && (!cached || refresh) {
		if len(s.folderSizePending) >= maxFolderSizeJobs {
			s.folderSizeMu.Unlock()
			return 0, "error", "folder-size queue is busy; try again", false
		}
		parent := ctx
		if lease != "" {
			parent = s.folderSizeContext
		}
		jobCtx, cancel := context.WithCancel(parent)
		job = &folderSizeJob{ctx: jobCtx, cancel: cancel, leases: make(map[string]time.Time)}
		s.folderSizePending[abs] = job
		s.folderSizeWait.Add(1)
		go s.calculateAndCacheFolderSize(abs, job)
	}
	if job != nil && lease != "" {
		if len(job.leases) < 64 || !job.leases[lease].IsZero() {
			job.leases[lease] = now.Add(folderSizeLeaseTTL)
		}
		if job.timer == nil {
			job.timer = time.AfterFunc(folderSizeLeaseTTL, func() { s.expireFolderSizeLeases(abs, job) })
		}
	}
	pending := job != nil
	s.folderSizeMu.Unlock()

	if cached {
		if entry.err != "" {
			return entry.size, "error", entry.err, pending
		}
		return entry.size, "ready", "", pending
	}
	if pending {
		return 0, "calculating", "", true
	}
	return 0, "error", "could not calculate folder size", false
}

func (s *Service) calculateAndCacheFolderSize(abs string, job *folderSizeJob) {
	defer s.folderSizeWait.Done()
	stop := context.AfterFunc(s.folderSizeContext, job.cancel)
	defer stop()
	defer job.cancel()
	var size int64
	var err error
	info, _ := s.resolver.disk.Stat(abs)
	select {
	case s.folderSizeSlots <- struct{}{}:
		size, err = calculateFolderSizeContext(job.ctx, abs)
		<-s.folderSizeSlots
	case <-job.ctx.Done():
		err = job.ctx.Err()
	}
	entry := folderSizeCacheEntry{size: size, calculatedAt: time.Now()}
	if info != nil {
		entry.directoryModified = info.ModTime()
	}
	if errors.Is(err, errFolderSizeIncomplete) {
		entry.incomplete = true
	} else if err != nil {
		entry.err = err.Error()
	}
	s.folderSizeMu.Lock()
	if job.timer != nil {
		job.timer.Stop()
	}
	if job.ctx.Err() == nil && !errors.Is(err, context.Canceled) {
		if len(s.folderSizeCache) >= 256 {
			oldest := abs
			for path, cached := range s.folderSizeCache {
				if cached.calculatedAt.Before(s.folderSizeCache[oldest].calculatedAt) || s.folderSizeCache[oldest].calculatedAt.IsZero() {
					oldest = path
				}
			}
			delete(s.folderSizeCache, oldest)
		}
		s.folderSizeCache[abs] = entry
	}
	delete(s.folderSizePending, abs)
	s.folderSizeMu.Unlock()
}

func calculateFolderSize(abs string) (int64, error) {
	return calculateFolderSizeContext(context.Background(), abs)
}

func calculateFolderSizeContext(ctx context.Context, abs string) (int64, error) {
	var size int64
	incomplete := false
	err := filepath.WalkDir(abs, func(path string, entry fs.DirEntry, err error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err != nil {
			if path == abs {
				return err
			}
			incomplete = true
			return nil
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			incomplete = true
			return nil
		}
		size += info.Size()
		return nil
	})
	if err == nil {
		err = ctx.Err()
	}
	if err == nil && incomplete {
		err = errFolderSizeIncomplete
	}
	return size, err
}

func (s *Service) expireFolderSizeLeases(abs string, job *folderSizeJob) {
	s.folderSizeMu.Lock()
	defer s.folderSizeMu.Unlock()
	if s.folderSizePending[abs] != job {
		return
	}
	for lease, until := range job.leases {
		if !time.Now().Before(until) {
			delete(job.leases, lease)
		}
	}
	if len(job.leases) == 0 {
		job.cancel()
		return
	}
	job.timer.Reset(folderSizeLeaseTTL)
}

func (s *Service) CancelFolderSizeLease(lease string) {
	if lease == "" {
		return
	}
	s.folderSizeMu.Lock()
	defer s.folderSizeMu.Unlock()
	for _, job := range s.folderSizePending {
		if _, exists := job.leases[lease]; exists {
			delete(job.leases, lease)
			if len(job.leases) == 0 {
				job.cancel()
			}
		}
	}
}

// Invalidate cached ancestors after known mutations. External changes are also
// detected through directory metadata and the five-minute freshness limit.
func (s *Service) InvalidateFolderSizes(paths ...string) {
	var changed []string
	for _, path := range paths {
		var absolute string
		var err error
		if IsConfiguredRootPath(path) {
			_, absolute, err = s.resolver.ResolveExisting(path)
		} else {
			_, absolute, err = s.resolver.ResolveEntry(path)
		}
		if err == nil {
			changed = append(changed, absolute)
		}
	}
	s.folderSizeMu.Lock()
	defer s.folderSizeMu.Unlock()
	for cached := range s.folderSizeCache {
		for _, path := range changed {
			if isSameOrDescendant(cached, path) {
				entry := s.folderSizeCache[cached]
				entry.dirty = true
				s.folderSizeCache[cached] = entry
				break
			}
		}
	}
	for cached, job := range s.folderSizePending {
		for _, path := range changed {
			if isSameOrDescendant(cached, path) {
				job.cancel()
				break
			}
		}
	}
}

func (s *Service) Chmod(ctx context.Context, virtualPath, modeText string) error {
	if IsConfiguredRootPath(virtualPath) {
		return errors.New("configured root permissions cannot be changed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	_, abs, err := s.resolver.ResolveEntry(virtualPath)
	if err != nil {
		return err
	}
	st, err := s.resolver.disk.Lstat(abs)
	if err != nil {
		return err
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return errors.New("symbolic-link permissions are not editable")
	}
	modeText = strings.TrimPrefix(strings.TrimSpace(modeText), "0o")
	modeText = strings.TrimPrefix(modeText, "0")
	if modeText == "" {
		modeText = "0"
	}
	value, err := strconv.ParseUint(modeText, 8, 12)
	if err != nil || value > 0o7777 {
		return fmt.Errorf("invalid mode %q", modeText)
	}
	mode := fs.FileMode(value & 0o777)
	if value&0o4000 != 0 {
		mode |= fs.ModeSetuid
	}
	if value&0o2000 != 0 {
		mode |= fs.ModeSetgid
	}
	if value&0o1000 != 0 {
		mode |= fs.ModeSticky
	}
	return s.resolver.disk.Chmod(abs, mode)
}

func unixPermissionBits(mode fs.FileMode) uint32 {
	bits := uint32(mode.Perm())
	if mode&fs.ModeSetuid != 0 {
		bits |= 0o4000
	}
	if mode&fs.ModeSetgid != 0 {
		bits |= 0o2000
	}
	if mode&fs.ModeSticky != 0 {
		bits |= 0o1000
	}
	return bits
}

func (s *Service) Chown(ctx context.Context, virtualPath, username, groupname string) error {
	if IsConfiguredRootPath(virtualPath) {
		return errors.New("configured root ownership cannot be changed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	_, abs, err := s.resolver.ResolveEntry(virtualPath)
	if err != nil {
		return err
	}
	uid, gid := -1, -1
	if strings.TrimSpace(username) != "" {
		u, err := user.Lookup(strings.TrimSpace(username))
		if err != nil {
			return err
		}
		parsed, err := strconv.Atoi(u.Uid)
		if err != nil {
			return err
		}
		uid = parsed
	}
	if strings.TrimSpace(groupname) != "" {
		g, err := user.LookupGroup(strings.TrimSpace(groupname))
		if err != nil {
			return err
		}
		parsed, err := strconv.Atoi(g.Gid)
		if err != nil {
			return err
		}
		gid = parsed
	}
	return s.resolver.disk.Lchown(abs, uid, gid)
}

func usernameForID(uid uint32) string {
	u, err := user.LookupId(strconv.FormatUint(uint64(uid), 10))
	if err != nil {
		return strconv.FormatUint(uint64(uid), 10)
	}
	return u.Username
}

func groupnameForID(gid uint32) string {
	g, err := user.LookupGroupId(strconv.FormatUint(uint64(gid), 10))
	if err != nil {
		return strconv.FormatUint(uint64(gid), 10)
	}
	return g.Name
}
