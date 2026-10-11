//go:build production

package main

import (
	"testing"

	"webfile/internal/config"
)

func TestProductionBuildRequiresAuthenticationAndSecureCookies(t *testing.T) {
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
	pamWithoutTLS := valid
	pamWithoutTLS.TLS = config.TLS{}
	if err := validateBuildConfig(pamWithoutTLS); err == nil {
		t.Fatal("non-TLS configuration was accepted")
	}
	proxy := valid
	proxy.Auth.Mode = "proxy"
	proxy.Listen = "127.0.0.1:8080"
	proxy.TLS = config.TLS{}
	if err := validateBuildConfig(proxy); err != nil {
		t.Fatalf("loopback proxy configuration was rejected: %v", err)
	}
	proxy.Listen = "0.0.0.0:8080"
	if err := validateBuildConfig(proxy); err == nil {
		t.Fatal("non-TLS proxy configuration on a public listener was accepted")
	}
	proxy.TLS.CertificateFile = "certificate"
	proxy.TLS.PrivateKeyFile = "key"
	if err := validateBuildConfig(proxy); err != nil {
		t.Fatalf("TLS proxy configuration was rejected: %v", err)
	}
	insecureCookie := valid
	insecureCookie.Auth.CookieSecure = false
	if err := validateBuildConfig(insecureCookie); err == nil {
		t.Fatal("insecure cookie configuration was accepted")
	}
}
