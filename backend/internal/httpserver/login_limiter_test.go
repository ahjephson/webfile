package httpserver

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLoginLimiterBoundsTrackedAttempts(t *testing.T) {
	target := newLoginLimiter(5, time.Minute, time.Minute)
	target.maxEntries = 2
	for _, address := range []string{"192.0.2.1:1", "192.0.2.2:1", "192.0.2.3:1"} {
		request := httptest.NewRequest("POST", "/api/login", nil)
		request.RemoteAddr = address
		target.Failure(request, "User")
	}
	if len(target.attempts) != 2 {
		t.Fatalf("tracked %d attempts", len(target.attempts))
	}
}

func TestLoginLimiterIgnoresForwardedAddress(t *testing.T) {
	target := newLoginLimiter(5, time.Minute, time.Minute)
	request := httptest.NewRequest("POST", "/api/login", nil)
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Set("X-Forwarded-For", "192.0.2.20")
	key := target.key(request, "User")
	if !strings.HasSuffix(key, "|127.0.0.1") {
		t.Fatalf("key=%q", key)
	}
}

func TestLoginLimiterReportsRemainingBlockDuration(t *testing.T) {
	target := newLoginLimiter(1, time.Minute, 5*time.Minute)
	request := httptest.NewRequest("POST", "/api/login", nil)
	request.RemoteAddr = "127.0.0.1:1234"
	target.Failure(request, "User")
	retryAfter := target.RetryAfter(request, "User")
	if retryAfter <= 4*time.Minute || retryAfter > 5*time.Minute {
		t.Fatalf("retry after=%s", retryAfter)
	}
	if target.Allowed(request, "User") {
		t.Fatal("blocked login was allowed")
	}
}
