//go:build linux

package launcher

import "testing"

func TestRandomElevationTokenIsValidAndUnique(t *testing.T) {
	first, err := RandomElevationToken()
	if err != nil {
		t.Fatal(err)
	}
	second, err := RandomElevationToken()
	if err != nil {
		t.Fatal(err)
	}
	if !validToken(first) || !validToken(second) {
		t.Fatalf("generated invalid tokens: %q %q", first, second)
	}
	if first == second {
		t.Fatal("generated duplicate elevation tokens")
	}
}

func TestValidTokenRejectsMalformedValues(t *testing.T) {
	for _, value := range []string{"", "abc", string(make([]byte, 64))} {
		if validToken(value) {
			t.Fatalf("accepted malformed token %q", value)
		}
	}
}
