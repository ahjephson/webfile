//go:build production

package main

import (
	"errors"
	"net"
	"net/http"
	"strings"

	"webfile/internal/config"
)

const buildMode = "production"

func validateBuildConfig(cfg config.Config) error {
	if cfg.Auth.Mode != "pam" && cfg.Auth.Mode != "proxy" {
		return errors.New("production builds require auth.mode pam or proxy")
	}
	if !cfg.Auth.CookieSecure {
		return errors.New("production builds require auth.cookie_secure true")
	}
	hasTLS := strings.TrimSpace(cfg.TLS.CertificateFile) != "" && strings.TrimSpace(cfg.TLS.PrivateKeyFile) != ""
	if cfg.Auth.Mode == "pam" && !hasTLS {
		return errors.New("production builds require a TLS certificate and private key")
	}
	if cfg.Auth.Mode == "proxy" && !hasTLS && !loopbackListenAddress(cfg.Listen) {
		return errors.New("production proxy authentication without direct TLS requires a loopback listen address")
	}
	return nil
}

func serveConfigured(server *http.Server, cfg config.Config) error {
	if strings.TrimSpace(cfg.TLS.CertificateFile) == "" {
		return server.ListenAndServe()
	}
	return server.ListenAndServeTLS(cfg.TLS.CertificateFile, cfg.TLS.PrivateKeyFile)
}

func loopbackListenAddress(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
