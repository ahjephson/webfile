package auth

import (
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// LoadProxySecret reads a private credential shared only with the authenticating
// proxy. IP allowlists alone cannot distinguish processes on a multi-user host.
func LoadProxySecret(path string) (string, error) {
	file, err := os.OpenFile(path, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return "", errors.New("cannot open proxy secret file")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0137 != 0 {
		return "", errors.New("proxy secret must be a regular file with mode 0600 or 0640")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 && stat.Uid != uint32(os.Geteuid()) {
		return "", errors.New("proxy secret must be owned by root or the service user")
	}
	data, err := io.ReadAll(io.LimitReader(file, 129))
	if err != nil || len(data) > 128 {
		return "", errors.New("cannot read proxy secret file")
	}
	secret := strings.TrimSpace(string(data))
	decoded, err := hex.DecodeString(secret)
	if err != nil || len(decoded) != 32 {
		return "", errors.New("proxy secret must contain exactly 64 hexadecimal characters")
	}
	return secret, nil
}
