//go:build production

package main

import (
	"errors"
	"net/http"
	"strings"

	"webfile/internal/config"
)

const buildMode = "production"

func validateBuildConfig(cfg config.Config) error {
	if cfg.Auth.Mode != "pam" {
		return errors.New("production builds require auth.mode pam")
	}
	if !cfg.Auth.CookieSecure {
		return errors.New("production builds require auth.cookie_secure true")
	}
	if strings.TrimSpace(cfg.TLS.CertificateFile) == "" || strings.TrimSpace(cfg.TLS.PrivateKeyFile) == "" {
		return errors.New("production builds require a TLS certificate and private key")
	}
	return nil
}

func serveConfigured(server *http.Server, cfg config.Config) error {
	return server.ListenAndServeTLS(cfg.TLS.CertificateFile, cfg.TLS.PrivateKeyFile)
}
