//go:build production

package main

import (
	"testing"

	"webfile/internal/config"
)

func TestProductionBuildRequiresPAMTLSAndSecureCookies(t *testing.T) {
	valid := config.Default()
	valid.Auth.Mode = "pam"
	valid.Auth.CookieSecure = true
	valid.TLS.CertificateFile = "certificate"
	valid.TLS.PrivateKeyFile = "key"
	if err := validateBuildConfig(valid); err != nil {
		t.Fatal(err)
	}
	noPAM := valid
	noPAM.Auth.Mode = "none"
	if err := validateBuildConfig(noPAM); err == nil {
		t.Fatal("no-auth configuration was accepted")
	}
	noTLS := valid
	noTLS.TLS = config.TLS{}
	if err := validateBuildConfig(noTLS); err == nil {
		t.Fatal("non-TLS configuration was accepted")
	}
	insecureCookie := valid
	insecureCookie.Auth.CookieSecure = false
	if err := validateBuildConfig(insecureCookie); err == nil {
		t.Fatal("insecure cookie configuration was accepted")
	}
}
