package rootedio

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

func TestGIVEN_ReplacedParent_WHEN_Mutating_THEN_OutsideRootIsUnchanged(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	parent := filepath.Join(root, "parent")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(outside, "sentinel")
	if err := os.WriteFile(sentinel, []byte("sentinel"), 0640); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "source")
	if err := os.WriteFile(source, []byte("source"), 0600); err != nil {
		t.Fatal(err)
	}
	disk := New(root)
	defer disk.Close()
	if err := os.Rename(parent, parent+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, parent); err != nil {
		t.Fatal(err)
	}
	for name, operation := range map[string]func() error{
		"write": func() error {
			file, err := disk.OpenFile(filepath.Join(parent, "sentinel"), os.O_WRONLY|os.O_TRUNC, 0)
			if file != nil {
				file.Close()
			}
			return err
		},
		"mkdir":      func() error { return disk.Mkdir(filepath.Join(parent, "new"), 0700) },
		"mkdir-all":  func() error { return disk.MkdirAll(filepath.Join(parent, "nested", "new"), 0700) },
		"chmod":      func() error { return disk.Chmod(filepath.Join(parent, "sentinel"), 0777) },
		"chown":      func() error { return disk.Lchown(filepath.Join(parent, "sentinel"), os.Getuid(), os.Getgid()) },
		"times":      func() error { return disk.Chtimes(filepath.Join(parent, "sentinel"), time.Unix(0, 0), time.Unix(0, 0)) },
		"remove":     func() error { return disk.Remove(filepath.Join(parent, "sentinel")) },
		"remove-all": func() error { return disk.RemoveAll(filepath.Join(parent, "sentinel")) },
		"link":       func() error { return disk.Link(source, filepath.Join(parent, "new")) },
		"rename":     func() error { return disk.Rename(source, filepath.Join(parent, "new")) },
		"publish":    func() error { return disk.RenameFlags(source, filepath.Join(parent, "new"), unix.RENAME_NOREPLACE) },
	} {
		t.Run(name, func(t *testing.T) {
			if err := operation(); err == nil {
				t.Fatal("accepted replaced parent")
			}
		})
	}
	data, err := os.ReadFile(sentinel)
	if err != nil || string(data) != "sentinel" {
		t.Fatalf("outside content changed: %q, %v", data, err)
	}
	info, err := os.Stat(sentinel)
	if err != nil || info.Mode().Perm() != 0640 {
		t.Fatalf("outside mode changed: %v, %v", info, err)
	}
}

func TestGIVEN_ConcurrentSymlinkExchange_WHEN_CreatingFiles_THEN_ConfinesWrites(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	parent, alternate := filepath.Join(root, "parent"), filepath.Join(root, "alternate")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, alternate); err != nil {
		t.Fatal(err)
	}
	disk := New(root)
	defer disk.Close()
	stop := make(chan struct{})
	var worker sync.WaitGroup
	worker.Add(1)
	go func() {
		defer worker.Done()
		for {
			select {
			case <-stop:
				return
			default:
				_ = unix.Renameat2(unix.AT_FDCWD, parent, unix.AT_FDCWD, alternate, unix.RENAME_EXCHANGE)
			}
		}
	}()
	for index := 0; index < 1000; index++ {
		file, err := disk.OpenFile(filepath.Join(parent, "created"), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
		if err == nil {
			_, _ = file.Write([]byte("content"))
			file.Close()
		}
	}
	close(stop)
	worker.Wait()
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("writes escaped root: %v, %v", entries, err)
	}
}

func TestGIVEN_FinalSymlink_WHEN_OpeningOrChmod_THEN_DoesNotChangeTarget(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	target := filepath.Join(outside, "sentinel")
	if err := os.WriteFile(target, []byte("sentinel"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	disk := New(root)
	defer disk.Close()
	file, err := disk.OpenFile(link, os.O_WRONLY|os.O_TRUNC, 0)
	if file != nil {
		file.Close()
	}
	if err == nil {
		t.Fatal("followed final symlink")
	}
	_ = disk.Chmod(link, 0777)
	info, err := os.Stat(target)
	if err != nil || info.Mode()&fs.ModePerm != 0600 || info.Size() != 8 {
		t.Fatalf("outside target changed: %v %v", info, err)
	}
}

func TestGIVEN_UnreadableFilesAndDirectories_WHEN_Chmod_THEN_UsesPinnedDescriptor(t *testing.T) {
	if os.Getenv("WEBFILE_TEST_NO_FCHMODAT2") != "1" {
		command := exec.Command(os.Args[0], "-test.run=^TestGIVEN_UnreadableFilesAndDirectories_WHEN_Chmod_THEN_UsesPinnedDescriptor$")
		command.Env = append(os.Environ(), "WEBFILE_TEST_NO_FCHMODAT2=1")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("legacy chmod subprocess: %v: %s", err, output)
		}
		return
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	filter := []unix.SockFilter{
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 0},
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, K: uint32(unix.SYS_FCHMODAT2), Jf: 1},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ERRNO | uint32(unix.ENOSYS)},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ALLOW},
	}
	program := unix.SockFprog{Len: uint16(len(filter)), Filter: &filter[0]}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := unix.Prctl(unix.PR_SET_SECCOMP, unix.SECCOMP_MODE_FILTER, uintptr(unsafe.Pointer(&program)), 0, 0); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	disk := New(root)
	defer disk.Close()
	for _, directory := range []bool{false, true} {
		path := filepath.Join(root, "file")
		if directory {
			path = filepath.Join(root, "directory")
			if err := os.Mkdir(path, 0700); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := os.WriteFile(path, []byte("content"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Chmod(path, 0); err != nil {
			t.Fatal(err)
		}
		if err := disk.Chmod(path, 0700); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0700 {
			t.Fatalf("permission change failed: %v %v", info, err)
		}
	}
}
