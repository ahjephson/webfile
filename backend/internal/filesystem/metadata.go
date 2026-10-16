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
	Path           string    `json:"path"`
	Name           string    `json:"name"`
	IsDir          bool      `json:"is_dir"`
	IsSymlink      bool      `json:"is_symlink"`
	Size           int64     `json:"size"`
	Modified       time.Time `json:"modified"`
	Mode           string    `json:"mode"`
	ModeOctal      string    `json:"mode_octal"`
	UID            uint32    `json:"uid"`
	GID            uint32    `json:"gid"`
	Owner          string    `json:"owner"`
	Group          string    `json:"group"`
	LinkTarget     string    `json:"link_target,omitempty"`
	SizeStatus     string    `json:"size_status,omitempty"`
	SizeError      string    `json:"size_error,omitempty"`
	SizeRefreshing bool      `json:"size_refreshing,omitempty"`
}

const folderSizeCacheTTL = 5 * time.Minute

type folderSizeCacheEntry struct {
	size         int64
	calculatedAt time.Time
	err          string
}

func (s *Service) applyCachedFolderSize(entry *Entry, abs string) {
	now := time.Now()
	s.folderSizeMu.Lock()
	cached, ok := s.folderSizeCache[abs]
	if ok && now.Sub(cached.calculatedAt) >= folderSizeCacheTTL {
		delete(s.folderSizeCache, abs)
		ok = false
	}
	s.folderSizeMu.Unlock()
	if ok && cached.err == "" {
		entry.Size = cached.size
		entry.SizeStatus = "ready"
	}
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
	st, err := os.Lstat(abs)
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
		d.LinkTarget, _ = os.Readlink(abs)
	}
	return d, nil
}

func (s *Service) DetailsWithFolderSize(ctx context.Context, virtualPath string, refresh bool) (Details, error) {
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
	size, status, sizeErr, refreshing := s.folderSize(abs, refresh)
	details.Size = size
	details.SizeStatus = status
	details.SizeError = sizeErr
	details.SizeRefreshing = refreshing
	return details, nil
}

func (s *Service) folderSize(abs string, refresh bool) (int64, string, string, bool) {
	now := time.Now()
	s.folderSizeMu.Lock()
	entry, cached := s.folderSizeCache[abs]
	if cached && now.Sub(entry.calculatedAt) >= folderSizeCacheTTL {
		delete(s.folderSizeCache, abs)
		cached = false
	}
	if !s.folderSizePending[abs] && (!cached || refresh) {
		s.folderSizePending[abs] = true
		go s.calculateAndCacheFolderSize(abs)
	}
	pending := s.folderSizePending[abs]
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

func (s *Service) calculateAndCacheFolderSize(abs string) {
	s.folderSizeSlots <- struct{}{}
	defer func() { <-s.folderSizeSlots }()
	size, err := calculateFolderSize(abs)
	entry := folderSizeCacheEntry{size: size, calculatedAt: time.Now()}
	if err != nil {
		entry.err = err.Error()
	}
	s.folderSizeMu.Lock()
	s.folderSizeCache[abs] = entry
	delete(s.folderSizePending, abs)
	s.folderSizeMu.Unlock()
}

func calculateFolderSize(abs string) (int64, error) {
	var size int64
	err := filepath.WalkDir(abs, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		size += info.Size()
		return nil
	})
	return size, err
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
	st, err := os.Lstat(abs)
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
	return os.Chmod(abs, mode)
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
	return os.Lchown(abs, uid, gid)
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
