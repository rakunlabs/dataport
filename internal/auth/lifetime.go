package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/rakunlabs/dataport/internal/store"
)

// lifetimeSetting holds the session lifetimes admins edit.
const lifetimeSetting = "session_lifetime"

// SessionLifetime decides how long sign-ins last. A session without
// "remember me" ends a fixed time after sign-in and its cookie ends with the
// browser. A remembered one stays while it is used: each visit extends it by
// the idle window, up to a maximum counted from sign-in.
type SessionLifetime struct {
	// Session is the fixed lifetime of a session without "remember me".
	Session Duration `json:"session"`
	// RememberIdle is how long a remembered session may go unused.
	RememberIdle Duration `json:"remember_idle"`
	// RememberMax caps a remembered session, counted from sign-in; zero
	// turns "remember me" off.
	RememberMax Duration `json:"remember_max"`
}

var DefaultSessionLifetime = SessionLifetime{
	Session:      Duration(24 * time.Hour),
	RememberIdle: Duration(7 * 24 * time.Hour),
	RememberMax:  Duration(30 * 24 * time.Hour),
}

// RememberEnabled reports whether sign-ins may be remembered.
func (l SessionLifetime) RememberEnabled() bool {
	return l.RememberMax > 0 && l.RememberIdle > 0
}

// CookieMaxAge is the cookie lifetime of a new session; zero ends it with
// the browser.
func (l SessionLifetime) CookieMaxAge(remember bool) time.Duration {
	if !remember || !l.RememberEnabled() {
		return 0
	}

	return time.Duration(min(l.RememberIdle, l.RememberMax))
}

// expiry is when a session created at created and used at seen ends, in Unix
// milliseconds.
func (l SessionLifetime) expiry(created, seen int64, remember bool) int64 {
	if remember && l.RememberEnabled() {
		return min(seen+time.Duration(l.RememberIdle).Milliseconds(), created+time.Duration(l.RememberMax).Milliseconds())
	}

	return created + time.Duration(l.Session).Milliseconds()
}

func (l SessionLifetime) Validate() error {
	const minimum = 5 * time.Minute

	if time.Duration(l.Session) < minimum {
		return errors.New("the session lifetime must be at least 5 minutes")
	}

	if l.RememberMax == 0 {
		return nil
	}

	if time.Duration(l.RememberIdle) < minimum {
		return errors.New("the remembered idle time must be at least 5 minutes")
	}

	if l.RememberMax < l.RememberIdle {
		return errors.New("the remembered maximum must be at least the idle time")
	}

	if l.RememberMax < l.Session {
		return errors.New("the remembered maximum must be at least the session lifetime")
	}

	return nil
}

// LoadLifetime reads the stored lifetimes into use; until an admin saves
// them the defaults apply.
func (a *Auth) LoadLifetime(ctx context.Context) error {
	raw, err := a.store.GetSetting(ctx, lifetimeSetting)
	if errors.Is(err, store.ErrNotFound) {
		a.SetLifetime(DefaultSessionLifetime)

		return nil
	}

	if err != nil {
		return err
	}

	l := DefaultSessionLifetime
	if err := json.Unmarshal([]byte(raw), &l); err != nil {
		return fmt.Errorf("decode session lifetime; %w", err)
	}

	a.SetLifetime(l)

	return nil
}

// SaveLifetime validates, stores and applies new lifetimes. Open sessions
// follow them from their next use.
func (a *Auth) SaveLifetime(ctx context.Context, l SessionLifetime) error {
	if err := l.Validate(); err != nil {
		return err
	}

	raw, err := json.Marshal(l)
	if err != nil {
		return err
	}

	if err := a.store.SetSetting(ctx, lifetimeSetting, string(raw)); err != nil {
		return err
	}

	a.SetLifetime(l)

	return nil
}

// Duration is a time.Duration that is whole minutes in JSON.
type Duration time.Duration

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(int64(time.Duration(d) / time.Minute))
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	var minutes int64
	if err := json.Unmarshal(b, &minutes); err != nil {
		return err
	}

	if minutes < 0 || minutes > 10*365*24*60 {
		return errors.New("duration out of range")
	}

	*d = Duration(time.Duration(minutes) * time.Minute)

	return nil
}
