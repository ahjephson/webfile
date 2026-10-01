package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Root struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type Archive struct {
	Executable         string   `json:"executable"`
	ExtractArgs        []string `json:"extract_args"`
	InspectArgs        []string `json:"inspect_args"`
	InspectPasswordArg string   `json:"inspect_password_arg"`
}

type TLS struct {
	CertificateFile string `json:"certificate_file"`
	PrivateKeyFile  string `json:"private_key_file"`
}

type Auth struct {
	Mode             string `json:"mode"`
	PAMService       string `json:"pam_service"`
	SessionTTL       string `json:"session_ttl"`
	CookieSecure     bool   `json:"cookie_secure"`
	LoginMaxAttempts int    `json:"login_max_attempts"`
	LoginWindow      string `json:"login_window"`
	LoginBlock       string `json:"login_block"`
}

type Runtime struct {
	WorkerDir      string `json:"worker_dir"`
	DataDir        string `json:"data_dir"`
	LauncherSocket string `json:"launcher_socket"`
	LauncherGroup  string `json:"launcher_group"`
	MinUserUID     uint32 `json:"min_user_uid"`
}

type Watch struct {
	Polling      string `json:"polling"`
	PollInterval string `json:"poll_interval"`
}

type Thumbnails struct {
	CacheDir            string `json:"cache_dir"`
	CacheSocket         string `json:"cache_socket"`
	CacheMaxBytes       int64  `json:"cache_max_bytes"`
	ImageWorkers        int    `json:"image_workers"`
	VideoWorkers        int    `json:"video_workers"`
	VideoFFmpegThreads  int    `json:"video_ffmpeg_threads"`
	VideoTimeoutSeconds int    `json:"video_timeout_seconds"`
	MaxQueued           int    `json:"max_queued"`
}

type Config struct {
	Listen         string     `json:"listen"`
	EditorMaxBytes int64      `json:"editor_max_bytes"`
	Roots          []Root     `json:"roots"`
	Archive        Archive    `json:"archive"`
	TLS            TLS        `json:"tls"`
	Auth           Auth       `json:"auth"`
	Runtime        Runtime    `json:"runtime"`
	Watch          Watch      `json:"watch"`
	Thumbnails     Thumbnails `json:"thumbnails"`
}

func Default() Config {
	return Config{
		Listen:         "127.0.0.1:8080",
		EditorMaxBytes: 5 << 20,
		Archive: Archive{
			Executable:         "7zz",
			ExtractArgs:        []string{"x", "-y", "-bsp1", "-bso0", "-bse2", "{archive}", "-o{destination}"},
			InspectArgs:        []string{"l", "-slt", "{archive}"},
			InspectPasswordArg: "-p",
		},
		Auth: Auth{
			Mode:             "none",
			PAMService:       "webfile",
			SessionTTL:       "12h",
			LoginMaxAttempts: 5,
			LoginWindow:      "1m",
			LoginBlock:       "5m",
		},
		Runtime: Runtime{
			WorkerDir:      "/run/webfile/users",
			DataDir:        "/var/lib/webfile",
			LauncherSocket: "/run/webfile/launcher.sock",
			LauncherGroup:  "webfile",
			MinUserUID:     1000,
		},
		Watch: Watch{Polling: "auto", PollInterval: "5s"},
		Thumbnails: Thumbnails{
			CacheDir:            "/var/cache/webfile/thumbnails",
			CacheSocket:         "/run/webfile/cache/thumbnails.sock",
			CacheMaxBytes:       10 << 30,
			ImageWorkers:        2,
			VideoWorkers:        2,
			VideoFFmpegThreads:  2,
			VideoTimeoutSeconds: 300,
			MaxQueued:           200,
		},
	}
}

func Load(path string) (Config, error) {
	cfg := Default()

	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c *Config) Validate() error {
	if strings.TrimSpace(c.Listen) == "" {
		return errors.New("listen is required")
	}
	if c.EditorMaxBytes <= 0 {
		return errors.New("editor_max_bytes must be greater than zero")
	}
	if len(c.Roots) == 0 {
		return errors.New("at least one root is required")
	}

	seen := map[string]struct{}{}
	for i := range c.Roots {
		r := &c.Roots[i]
		r.Name = strings.TrimSpace(r.Name)
		if r.Name == "" || strings.ContainsAny(r.Name, `/\\`) {
			return fmt.Errorf("invalid root name %q", r.Name)
		}
		if _, ok := seen[strings.ToLower(r.Name)]; ok {
			return fmt.Errorf("duplicate root name %q", r.Name)
		}
		seen[strings.ToLower(r.Name)] = struct{}{}

		if strings.TrimSpace(r.Path) == "" {
			return fmt.Errorf("root %q has no path", r.Name)
		}
		if !strings.Contains(r.Path, "{home}") && !strings.Contains(r.Path, "{user}") {
			abs, err := filepath.Abs(r.Path)
			if err != nil {
				return fmt.Errorf("root %q: %w", r.Name, err)
			}
			r.Path = filepath.Clean(abs)
		}
	}

	if c.Archive.Executable != "" && len(c.Archive.ExtractArgs) == 0 {
		return errors.New("archive.extract_args is required when archive.executable is set")
	}
	if c.Archive.Executable != "" && len(c.Archive.InspectArgs) == 0 {
		return errors.New("archive.inspect_args is required when archive.executable is set")
	}
	if strings.Contains(c.Archive.InspectPasswordArg, "{password}") {
		return errors.New("archive.inspect_password_arg must be a non-secret password prefix and must not contain {password}")
	}
	if (strings.TrimSpace(c.TLS.CertificateFile) == "") != (strings.TrimSpace(c.TLS.PrivateKeyFile) == "") {
		return errors.New("tls.certificate_file and tls.private_key_file must be configured together")
	}
	if c.Auth.Mode != "none" && c.Auth.Mode != "pam" {
		return fmt.Errorf("unsupported auth.mode %q", c.Auth.Mode)
	}
	if c.Auth.PAMService == "" {
		c.Auth.PAMService = "webfile"
	}
	if c.Auth.SessionTTL == "" {
		c.Auth.SessionTTL = "12h"
	}
	if c.Auth.LoginMaxAttempts <= 0 {
		c.Auth.LoginMaxAttempts = 5
	}
	if c.Auth.LoginWindow == "" {
		c.Auth.LoginWindow = "1m"
	}
	if c.Auth.LoginBlock == "" {
		c.Auth.LoginBlock = "5m"
	}
	if c.Runtime.WorkerDir == "" {
		c.Runtime.WorkerDir = "/run/webfile/users"
	}
	if c.Runtime.DataDir == "" {
		c.Runtime.DataDir = "/var/lib/webfile"
	}
	if c.Runtime.LauncherSocket == "" {
		c.Runtime.LauncherSocket = "/run/webfile/launcher.sock"
	}
	if c.Runtime.LauncherGroup == "" {
		c.Runtime.LauncherGroup = "webfile"
	}
	if c.Runtime.MinUserUID == 0 {
		c.Runtime.MinUserUID = 1000
	}
	if c.Watch.Polling == "" {
		c.Watch.Polling = "auto"
	}
	if c.Watch.Polling != "auto" && c.Watch.Polling != "always" && c.Watch.Polling != "disabled" {
		return fmt.Errorf("unsupported watch.polling %q", c.Watch.Polling)
	}
	if c.Watch.PollInterval == "" {
		c.Watch.PollInterval = "5s"
	}
	if strings.TrimSpace(c.Thumbnails.CacheDir) == "" || !filepath.IsAbs(c.Thumbnails.CacheDir) {
		return errors.New("thumbnails.cache_dir must be an absolute path")
	}
	c.Thumbnails.CacheDir = filepath.Clean(c.Thumbnails.CacheDir)
	if strings.TrimSpace(c.Thumbnails.CacheSocket) == "" || !filepath.IsAbs(c.Thumbnails.CacheSocket) {
		return errors.New("thumbnails.cache_socket must be an absolute path")
	}
	c.Thumbnails.CacheSocket = filepath.Clean(c.Thumbnails.CacheSocket)
	if c.Thumbnails.CacheMaxBytes <= 0 {
		return errors.New("thumbnails.cache_max_bytes must be greater than zero")
	}
	if c.Thumbnails.ImageWorkers <= 0 || c.Thumbnails.VideoWorkers <= 0 || c.Thumbnails.VideoFFmpegThreads <= 0 {
		return errors.New("thumbnail worker counts must be greater than zero")
	}
	if c.Thumbnails.VideoTimeoutSeconds <= 0 {
		return errors.New("thumbnails.video_timeout_seconds must be greater than zero")
	}
	if c.Thumbnails.MaxQueued <= 0 {
		return errors.New("thumbnails.max_queued must be greater than zero")
	}
	return nil
}

func (c Config) RootsForUser(username, home string) ([]Root, error) {
	out := make([]Root, 0, len(c.Roots))
	for _, root := range c.Roots {
		p := strings.ReplaceAll(root.Path, "{home}", home)
		p = strings.ReplaceAll(p, "{user}", username)
		if !filepath.IsAbs(p) {
			return nil, fmt.Errorf("root %q does not resolve to an absolute path", root.Name)
		}
		root.Path = filepath.Clean(p)
		out = append(out, root)
	}
	return out, nil
}
