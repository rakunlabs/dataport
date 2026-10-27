package auth

import (
	"encoding/json"
	"testing"
	"time"
)

func TestPasswordHash(t *testing.T) {
	hash, err := HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}

	if !VerifyPassword("correct horse", hash) {
		t.Fatal("password should verify")
	}

	if VerifyPassword("wrong", hash) {
		t.Fatal("wrong password verified")
	}

	if VerifyPassword("correct horse", "garbage") {
		t.Fatal("malformed hash verified")
	}
}

func TestSessionLifetime(t *testing.T) {
	const h = int64(time.Hour / time.Millisecond)

	l := SessionLifetime{Session: Duration(24 * time.Hour), RememberIdle: Duration(7 * 24 * time.Hour), RememberMax: Duration(30 * 24 * time.Hour)}

	// A session without "remember me" ends a fixed time after sign-in,
	// however much it is used.
	if got := l.expiry(0, 20*h, false); got != 24*h {
		t.Fatalf("plain session: %d", got)
	}

	// A remembered one slides by the idle window...
	if got := l.expiry(0, 48*h, true); got != (48+7*24)*h {
		t.Fatalf("remembered slide: %d", got)
	}

	// ...but never past the maximum from sign-in.
	if got := l.expiry(0, 28*24*h, true); got != 30*24*h {
		t.Fatalf("remembered cap: %d", got)
	}

	if l.CookieMaxAge(false) != 0 || l.CookieMaxAge(true) != 7*24*time.Hour {
		t.Fatal("cookie max age")
	}

	off := l
	off.RememberMax = 0
	if off.RememberEnabled() || off.expiry(0, 10*h, true) != 24*h || off.Validate() != nil {
		t.Fatal("remember off")
	}

	for _, bad := range []SessionLifetime{
		{Session: Duration(time.Minute)},
		{Session: Duration(time.Hour), RememberIdle: Duration(48 * time.Hour), RememberMax: Duration(24 * time.Hour)},
		{Session: Duration(48 * time.Hour), RememberIdle: Duration(time.Hour), RememberMax: Duration(24 * time.Hour)},
	} {
		if bad.Validate() == nil {
			t.Errorf("accepted %+v", bad)
		}
	}

	var d Duration
	if err := json.Unmarshal([]byte(`90`), &d); err != nil || time.Duration(d) != 90*time.Minute {
		t.Fatalf("minutes: %v %v", d, err)
	}
}
