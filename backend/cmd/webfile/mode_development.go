//go:build !production

package main

import (
	"errors"
	"net/http"

	"webfile/internal/config"
)

const buildMode = "development"

func validateBuildConfig(cfg config.Config) error {
	if cfg.Auth.Mode != "none" {
		return errors.New("development builds require auth.mode none")
	}
	if cfg.TLS.CertificateFile != "" || cfg.TLS.PrivateKeyFile != "" {
		return errors.New("development builds use HTTP and do not accept TLS configuration")
	}
	return nil
}

func serveConfigured(server *http.Server, _ config.Config) error {
	return server.ListenAndServe()
}
