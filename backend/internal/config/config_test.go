package config

import "testing"

func TestDefaultElevationTTLIsShortLived(t *testing.T) {
	if got := Default().Auth.ElevationTTL; got != "5m" {
		t.Fatalf("elevation ttl = %q", got)
	}
}

func TestValidateRequiresCompleteTLSKeyPair(t *testing.T) {
	target := Default()
	target.Roots = []Root{{Name: "Root", Path: "/"}}
	target.TLS.CertificateFile = "certificate"
	if err := target.Validate(); err == nil {
		t.Fatal("incomplete TLS configuration was accepted")
	}
}

func TestValidateRejectsPasswordInArchiveArguments(t *testing.T) {
	target := Default()
	target.Roots = []Root{{Name: "Root", Path: "/"}}
	target.Archive.InspectPasswordArg = "-p{password}"
	if err := target.Validate(); err == nil {
		t.Fatal("password placeholder was accepted")
	}
}

func TestRootsForUserExpandsLinuxIdentityPlaceholders(t *testing.T) {
	cfg := Config{Roots: []Root{
		{Name: "Home", Path: "{home}"},
		{Name: "Shared", Path: "/srv/users/{user}"},
	}}
	roots, err := cfg.RootsForUser("alex", "/home/alex")
	if err != nil {
		t.Fatal(err)
	}
	if roots[0].Path != "/home/alex" {
		t.Fatalf("home = %q", roots[0].Path)
	}
	if roots[1].Path != "/srv/users/alex" {
		t.Fatalf("shared = %q", roots[1].Path)
	}
}

func TestValidateRejectsInvalidExcludedNames(t *testing.T) {
	target := Default()
	target.Roots = []Root{{Name: "Root", Path: "/", ExcludedNames: []string{"valid", "nested/name"}}}
	if err := target.Validate(); err == nil {
		t.Fatal("excluded name containing a path separator was accepted")
	}
}

func TestValidateNormalizesExcludedPaths(t *testing.T) {
	target := Default()
	target.Roots = []Root{{Name: "Root", Path: "/", ExcludedPaths: []string{"var/cache/hidden/"}}}
	if err := target.Validate(); err != nil {
		t.Fatal(err)
	}
	if got := target.Roots[0].ExcludedPaths[0]; got != "/var/cache/hidden" {
		t.Fatalf("excluded path = %q", got)
	}
}

func TestValidateRejectsInvalidExcludedPaths(t *testing.T) {
	for _, excludedPath := range []string{"/", "../secret", "nested/../secret", `nested\secret`} {
		t.Run(excludedPath, func(t *testing.T) {
			target := Default()
			target.Roots = []Root{{Name: "Root", Path: "/", ExcludedPaths: []string{excludedPath}}}
			if err := target.Validate(); err == nil {
				t.Fatalf("excluded path %q was accepted", excludedPath)
			}
		})
	}
}

func TestValidateRejectsDuplicateNormalizedExcludedPaths(t *testing.T) {
	target := Default()
	target.Roots = []Root{{Name: "Root", Path: "/", ExcludedPaths: []string{"var/cache", "/var/cache/"}}}
	if err := target.Validate(); err == nil {
		t.Fatal("duplicate excluded paths were accepted")
	}
}

func TestValidateProxyAuthentication(t *testing.T) {
	target := Default()
	target.Roots = []Root{{Name: "Root", Path: "/"}}
	target.Auth.Mode = "proxy"
	target.Auth.TrustedProxies = []string{"127.0.0.1/32", "2001:db8::1/64"}
	target.Auth.LogoutURL = "/_auth/logout?return=/"
	if err := target.Validate(); err != nil {
		t.Fatal(err)
	}
	if target.Auth.UserHeader != "X-Authenticated-User" {
		t.Fatalf("user header = %q", target.Auth.UserHeader)
	}
	if target.Auth.TrustedProxies[1] != "2001:db8::/64" {
		t.Fatalf("normalized trusted proxy = %q", target.Auth.TrustedProxies[1])
	}
}

func TestValidateProxyAuthenticationRequiresTrustedProxy(t *testing.T) {
	target := Default()
	target.Roots = []Root{{Name: "Root", Path: "/"}}
	target.Auth.Mode = "proxy"
	if err := target.Validate(); err == nil {
		t.Fatal("proxy authentication without a trusted proxy was accepted")
	}
}

func TestValidateRejectsUnsafeProxyAuthenticationValues(t *testing.T) {
	for name, configure := range map[string]func(*Config){
		"header":     func(target *Config) { target.Auth.UserHeader = "Bad Header" },
		"header-del": func(target *Config) { target.Auth.UserHeader = "Bad\x7fHeader" },
		"proxy":      func(target *Config) { target.Auth.TrustedProxies = []string{"not-a-cidr"} },
		"logout":     func(target *Config) { target.Auth.LogoutURL = "http://example.com/logout" },
	} {
		t.Run(name, func(t *testing.T) {
			target := Default()
			target.Roots = []Root{{Name: "Root", Path: "/"}}
			target.Auth.Mode = "proxy"
			target.Auth.TrustedProxies = []string{"127.0.0.1/32"}
			configure(&target)
			if err := target.Validate(); err == nil {
				t.Fatal("unsafe proxy authentication configuration was accepted")
			}
		})
	}
}
