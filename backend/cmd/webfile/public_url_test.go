package main

import (
	"reflect"
	"testing"
)

func TestResolveListenAddress(t *testing.T) {
	resolved, err := resolveListenAddress("127.0.0.1:8080", "", "127.0.0.2", 6201)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != "127.0.0.2:6201" {
		t.Fatalf("resolved address = %q", resolved)
	}
	resolved, err = resolveListenAddress("127.0.0.1:8080", "[::1]:6201", "", 0)
	if err != nil || resolved != "[::1]:6201" {
		t.Fatalf("--http resolution = %q, %v", resolved, err)
	}
}

func TestResolvePublicURLDerivesMountAndOrigin(t *testing.T) {
	publicURL, basePath, origins, err := resolvePublicAddress("https://192.168.0.254/tools/webfile/", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if publicURL != "https://192.168.0.254/tools/webfile" || basePath != "/tools/webfile" || !reflect.DeepEqual(origins, []string{"https://192.168.0.254"}) {
		t.Fatalf("resolved public address = %q, %q, %#v", publicURL, basePath, origins)
	}
}

func TestResolveExplicitBasePathAndOrigins(t *testing.T) {
	publicURL, basePath, origins, err := resolvePublicAddress("", "/tools/webfile/", []string{"https://192.168.0.254", "https://files.example.test/"})
	if err != nil {
		t.Fatal(err)
	}
	if publicURL != "" || basePath != "/tools/webfile" || !reflect.DeepEqual(origins, []string{"https://192.168.0.254", "https://files.example.test"}) {
		t.Fatalf("resolved explicit address = %q, %q, %#v", publicURL, basePath, origins)
	}
}

func TestResolvePublicAddressRejectsAmbiguousOptions(t *testing.T) {
	if _, _, _, err := resolvePublicAddress("https://example.test/webfile", "/webfile", nil); err == nil {
		t.Fatal("combined --public-url and --base-path were accepted")
	}
}
