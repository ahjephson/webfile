package config

import "testing"

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
