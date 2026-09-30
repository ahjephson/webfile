//go:build !production

package main

import (
	"testing"

	"webfile/internal/config"
)

func TestDevelopmentBuildAcceptsOnlyHTTPWithoutAuthentication(t *testing.T) {
	valid := config.Default()
	if err := validateBuildConfig(valid); err != nil {
		t.Fatal(err)
	}
	pam := valid
	pam.Auth.Mode = "pam"
	if err := validateBuildConfig(pam); err == nil {
		t.Fatal("PAM configuration was accepted")
	}
	tlsConfig := valid
	tlsConfig.TLS.CertificateFile = "certificate"
	tlsConfig.TLS.PrivateKeyFile = "key"
	if err := validateBuildConfig(tlsConfig); err == nil {
		t.Fatal("TLS configuration was accepted")
	}
}
