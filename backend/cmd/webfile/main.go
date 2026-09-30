package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"webfile/internal/archive"
	"webfile/internal/auth"
	"webfile/internal/config"
	"webfile/internal/filesystem"
	"webfile/internal/httpserver"
	"webfile/internal/identity"
	"webfile/internal/launcher"
	"webfile/internal/operations"
	"webfile/internal/persistence"
	"webfile/internal/session"
	"webfile/internal/watch"
	"webfile/internal/worker"
)

var (
	version   = "dev"
	commit    = "unknown"
	buildDate = "unknown"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if len(os.Args) < 2 {
		serve(os.Args[1:])
		return
	}
	switch os.Args[1] {
	case "serve":
		serve(os.Args[2:])
	case "worker":
		workerMain(os.Args[2:])
	case "launcher":
		launcherMain(os.Args[2:])
	case "version", "--version", "-version":
		fmt.Printf("webfile %s (commit %s, built %s)\n", version, commit, buildDate)
	default:
		serve(os.Args[1:])
	}
}

func serve(args []string) {
	flags := flag.NewFlagSet("serve", flag.ExitOnError)
	configPath := flags.String("config", "webfile.json", "path to configuration file")
	_ = flags.Parse(args)

	absConfig, err := filepath.Abs(*configPath)
	fatalIf(err, "resolve config path")
	cfg, err := config.Load(absConfig)
	fatalIf(err, "configuration failed")
	fatalIf(validateBuildConfig(cfg), "configuration is incompatible with this "+buildMode+" build")

	ttl, err := time.ParseDuration(cfg.Auth.SessionTTL)
	fatalIf(err, "invalid auth.session_ttl")
	if ttl <= 0 {
		fatalIf(errors.New("auth.session_ttl must be greater than zero"), "configuration failed")
	}
	loginWindow, err := time.ParseDuration(cfg.Auth.LoginWindow)
	fatalIf(err, "invalid auth.login_window")
	loginBlock, err := time.ParseDuration(cfg.Auth.LoginBlock)
	fatalIf(err, "invalid auth.login_block")
	if cfg.Auth.Mode == "pam" && !auth.PAMAvailable() {
		fatalIf(errors.New("auth.mode is pam but PAM support is not compiled in; rebuild with CGO_ENABLED=1 and -tags pam"), "authentication unavailable")
	}

	state, err := persistence.Open(filepath.Join(cfg.Runtime.DataDir, "state.json"))
	fatalIf(err, "open persistent state")
	sessions := session.New(ttl, cfg.Auth.CookieSecure, state)
	_ = sessions.Sweep()

	executable, err := os.Executable()
	fatalIf(err, "resolve executable")
	workerManager := worker.NewManager(executable, absConfig, cfg.Runtime.WorkerDir, cfg.Runtime.LauncherSocket)
	defer workerManager.Close()

	currentIdentity, err := identity.Current()
	fatalIf(err, "resolve current user")
	mainServer := httpserver.NewMain(httpserver.MainOptions{
		AuthMode: cfg.Auth.Mode,
		Authenticate: func(ctx context.Context, username, password string) error {
			return launcher.ClientAuthenticate(ctx, cfg.Runtime.LauncherSocket, username, password)
		},
		Sessions:         sessions,
		State:            state,
		Workers:          workerManager,
		CurrentUser:      currentIdentity,
		CookieSecure:     cfg.Auth.CookieSecure,
		LoginMaxAttempts: cfg.Auth.LoginMaxAttempts,
		LoginWindow:      loginWindow,
		LoginBlock:       loginBlock,
	})

	httpServer := &http.Server{
		Addr:              cfg.Listen,
		Handler:           mainServer.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
		TLSConfig:         &tls.Config{MinVersion: tls.VersionTLS12},
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				sessions.Sweep()
			}
		}
	}()

	slog.Info("webfile listening", slog.String("address", cfg.Listen), slog.String("auth", cfg.Auth.Mode), slog.String("build_mode", buildMode), slog.String("version", version))
	if err := serveConfigured(httpServer, cfg); err != nil && err != http.ErrServerClosed {
		fatalIf(err, "server stopped")
	}
}

func launcherMain(args []string) {
	flags := flag.NewFlagSet("launcher", flag.ExitOnError)
	configPath := flags.String("config", "webfile.json", "path to configuration file")
	_ = flags.Parse(args)
	if os.Geteuid() != 0 {
		fatalIf(errors.New("launcher must run as root"), "launcher configuration failed")
	}
	absConfig, err := filepath.Abs(*configPath)
	fatalIf(err, "resolve config path")
	cfg, err := config.Load(absConfig)
	fatalIf(err, "launcher configuration failed")
	fatalIf(validateBuildConfig(cfg), "launcher configuration is incompatible with this "+buildMode+" build")
	executable, err := os.Executable()
	fatalIf(err, "resolve executable")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv := launcher.Server{
		Executable:  executable,
		ConfigPath:  absConfig,
		SocketPath:  cfg.Runtime.LauncherSocket,
		WorkerDir:   cfg.Runtime.WorkerDir,
		BrokerGroup: cfg.Runtime.LauncherGroup,
		MinUserUID:  cfg.Runtime.MinUserUID,
		PAMService:  cfg.Auth.PAMService,
	}
	slog.Info("worker launcher ready", slog.String("socket", cfg.Runtime.LauncherSocket))
	fatalIf(srv.Serve(ctx), "launcher stopped")
}

func workerMain(args []string) {
	flags := flag.NewFlagSet("worker", flag.ExitOnError)
	configPath := flags.String("config", "webfile.json", "path to configuration file")
	socketPath := flags.String("socket", "", "unix socket path")
	username := flags.String("username", "", "linux username")
	home := flags.String("home", "", "linux home directory")
	_ = flags.Parse(args)

	if *socketPath == "" || *username == "" || *home == "" {
		fatalIf(errors.New("worker requires -socket, -username and -home"), "worker configuration failed")
	}
	cfg, err := config.Load(*configPath)
	fatalIf(err, "worker configuration failed")
	fatalIf(validateBuildConfig(cfg), "worker configuration is incompatible with this "+buildMode+" build")
	rootCfg, err := cfg.RootsForUser(*username, *home)
	fatalIf(err, "resolve worker roots")
	roots := make([]filesystem.Root, 0, len(rootCfg))
	for _, r := range rootCfg {
		roots = append(roots, filesystem.Root{Name: r.Name, Path: r.Path})
	}
	resolver, err := filesystem.NewResolver(roots)
	fatalIf(err, "filesystem resolver failed")
	files := filesystem.NewService(resolver, cfg.EditorMaxBytes)
	uploads := filesystem.NewUploadManager(resolver, filepath.Join(*home, ".local", "share", "webfile", "uploads"))
	pollInterval, err := time.ParseDuration(cfg.Watch.PollInterval)
	fatalIf(err, "invalid watch.poll_interval")
	watchManager, err := watch.New(watch.Options{Polling: watch.PollingMode(cfg.Watch.Polling), PollInterval: pollInterval})
	fatalIf(err, "filesystem watcher failed")
	defer watchManager.Close()
	extractor := archive.New(archive.Config{
		Executable:         cfg.Archive.Executable,
		ExtractArgs:        cfg.Archive.ExtractArgs,
		InspectArgs:        cfg.Archive.InspectArgs,
		InspectPasswordArg: cfg.Archive.InspectPasswordArg,
	}, resolver)
	taskDatabase := filepath.Join(*home, ".local", "share", "webfile", "tasks.db")
	opManager, err := operations.Open(taskDatabase, 100, 2)
	fatalIf(err, "open task database")
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer closeCancel()
		if err := opManager.Close(closeCtx); err != nil {
			slog.Error("close task manager", slog.String("err", err.Error()))
		}
	}()
	app := httpserver.NewWorker(files, uploads, watchManager, extractor, opManager)

	_ = os.Remove(*socketPath)
	listener, err := net.Listen("unix", *socketPath)
	fatalIf(err, "worker listen failed")
	defer listener.Close()
	_ = os.Chmod(*socketPath, 0o600)

	httpServer := &http.Server{Handler: app.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()

	slog.Info("filesystem worker ready", slog.String("user", *username), slog.Int("uid", os.Geteuid()))
	if err := httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
		fatalIf(err, "worker stopped")
	}
}

func fatalIf(err error, message string) {
	if err == nil {
		return
	}
	slog.Error(message, slog.String("err", err.Error()))
	os.Exit(1)
}
