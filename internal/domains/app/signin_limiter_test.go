package app

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestSigninLimiterUsesFixedWindowPerRemoteAddress(t *testing.T) {
	limiter := newSigninLimiter()
	now := time.Unix(1_700_000_000, 0)
	limiter.now = func() time.Time { return now }
	request := httptest.NewRequest("POST", "/app/user/signin", nil)
	request.RemoteAddr = "192.0.2.1:12345"
	for attempt := 0; attempt < signinWindowLimit; attempt++ {
		if !limiter.allow(request) {
			t.Fatalf("attempt %d was denied too early", attempt+1)
		}
	}
	if limiter.allow(request) {
		t.Fatal("request beyond the limit was allowed")
	}
	now = now.Add(signinWindow)
	if !limiter.allow(request) {
		t.Fatal("new window did not reset the limiter")
	}
}
