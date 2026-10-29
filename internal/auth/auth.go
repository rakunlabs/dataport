// Package auth hashes passwords and manages database-backed sessions.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/rakunlabs/dataport/internal/store"
)

const (
	argonTime    = 2
	argonMemory  = 64 * 1024
	argonThreads = 2
	argonKeyLen  = 32

	MinPasswordLength = 8
)

var (
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrLocked             = errors.New("too many failed attempts, try again later")
	ErrWeakPassword       = fmt.Errorf("password must be at least %d characters", MinPasswordLength)
)

func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}

	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func VerifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}

	var (
		memory  uint32
		t       uint32
		threads uint8
	)

	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &t, &threads); err != nil {
		return false
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}

	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}

	got := argon2.IDKey([]byte(password), salt, t, memory, threads, uint32(len(want)))

	return subtle.ConstantTimeCompare(got, want) == 1
}

func ValidatePassword(password string) error {
	if len(password) < MinPasswordLength {
		return ErrWeakPassword
	}

	return nil
}

// RandomPassword returns a URL-safe random password.
func RandomPassword() string {
	b := make([]byte, 15)
	_, _ = rand.Read(b)

	return base64.RawURLEncoding.EncodeToString(b)
}

// ////////////////////////////////////////////

type Auth struct {
	store   *store.Store
	limiter *limiter

	// Session lifetimes, edited by admins and kept in the store.
	mu       sync.RWMutex
	lifetime SessionLifetime

	// dummyHash keeps a failed login for an unknown user as slow as one for a
	// known user, so usernames cannot be probed by timing.
	dummyHash string
}

func New(st *store.Store) *Auth {
	dummy, _ := HashPassword(RandomPassword())

	return &Auth{
		store:     st,
		lifetime:  DefaultSessionLifetime,
		limiter:   newLimiter(5, time.Minute),
		dummyHash: dummy,
	}
}

// Lifetime returns the session lifetimes in use.
func (a *Auth) Lifetime() SessionLifetime {
	a.mu.RLock()
	defer a.mu.RUnlock()

	return a.lifetime
}

// SetLifetime replaces the session lifetimes in use.
func (a *Auth) SetLifetime(l SessionLifetime) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.lifetime = l
}

// Login checks the credentials and opens a session. It returns the raw
// session token that goes into the cookie.
func (a *Auth) Login(ctx context.Context, username, password, ip, userAgent string, remember bool) (string, *store.User, error) {
	username = strings.TrimSpace(username)
	key := strings.ToLower(username) + "|" + ip

	if a.limiter.locked(key) {
		return "", nil, ErrLocked
	}

	user, err := a.store.GetUserByUsername(ctx, username)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return "", nil, err
	}

	if user == nil {
		VerifyPassword(password, a.dummyHash)
		a.limiter.fail(key)

		return "", nil, ErrInvalidCredentials
	}

	if !VerifyPassword(password, user.PasswordHash) || user.Disabled {
		a.limiter.fail(key)

		return "", nil, ErrInvalidCredentials
	}

	a.limiter.reset(key)

	token, err := a.CreateSession(ctx, user.ID, ip, userAgent, remember)
	if err != nil {
		return "", nil, err
	}

	_ = a.store.TouchUserLogin(ctx, user.ID)

	return token, user, nil
}

// CreateSession opens a session; a remembered one lives for the longer
// remember-me lifetime.
func (a *Auth) CreateSession(ctx context.Context, userID, ip, userAgent string, remember bool) (string, error) {
	l := a.Lifetime()
	remember = remember && l.RememberEnabled()

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}

	token := base64.RawURLEncoding.EncodeToString(raw)
	ts := time.Now()

	if len(userAgent) > 256 {
		userAgent = userAgent[:256]
	}

	if err := a.store.CreateSession(ctx, &store.Session{
		TokenHash:  hashToken(token),
		UserID:     userID,
		CreatedAt:  ts.UnixMilli(),
		ExpiresAt:  l.expiry(ts.UnixMilli(), ts.UnixMilli(), remember),
		LastSeenAt: ts.UnixMilli(),
		IP:         ip,
		UserAgent:  userAgent,
		Remember:   remember,
	}); err != nil {
		return "", fmt.Errorf("create session; %w", err)
	}

	return token, nil
}

// Authenticate resolves a session token to its user. A remembered session
// slides: each use more than a minute after the last one extends its expiry
// by the idle window, never past its maximum lifetime.
func (a *Auth) Authenticate(ctx context.Context, token string) (*store.User, error) {
	user, _, err := a.AuthenticateSession(ctx, token)

	return user, err
}

// AuthenticateSession is Authenticate that also returns the session, whose
// Remember decides the cookie's lifetime. LastSeenAt is the previous use and
// ExpiresAt the extended expiry.
func (a *Auth) AuthenticateSession(ctx context.Context, token string) (*store.User, *store.Session, error) {
	if token == "" {
		return nil, nil, store.ErrNotFound
	}

	hash := hashToken(token)

	sess, err := a.store.GetSession(ctx, hash)
	if err != nil {
		return nil, nil, err
	}

	ts := time.Now()
	if ts.UnixMilli() > sess.ExpiresAt {
		_ = a.store.DeleteSession(ctx, hash)

		return nil, nil, store.ErrNotFound
	}

	user, err := a.store.GetUser(ctx, sess.UserID)
	if err != nil {
		return nil, nil, err
	}

	if user.Disabled {
		return nil, nil, store.ErrNotFound
	}

	if ts.UnixMilli()-sess.LastSeenAt > time.Minute.Milliseconds() {
		// Recomputed from the current lifetimes, so an admin's change applies
		// to open sessions too.
		expires := a.Lifetime().expiry(sess.CreatedAt, ts.UnixMilli(), sess.Remember)
		if ts.UnixMilli() > expires {
			_ = a.store.DeleteSession(ctx, hash)

			return nil, nil, store.ErrNotFound
		}
		if err := a.store.TouchSession(ctx, hash, ts.UnixMilli(), expires); err == nil {
			sess.ExpiresAt = expires
		}
	}

	return user, sess, nil
}

func (a *Auth) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}

	return a.store.DeleteSession(ctx, hashToken(token))
}

// Janitor removes expired sessions and picks up lifetimes saved on another
// replica, until ctx is done.
func (a *Auth) Janitor(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for n := 1; ; n++ {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := a.LoadLifetime(ctx); err != nil && ctx.Err() == nil {
				slog.Error("load session lifetime", "error", err)
			}

			if n%15 != 0 {
				continue
			}

			_, _ = a.store.DeleteExpiredSessions(ctx)
			_, _ = a.store.DeleteExpiredOAuthCodes(ctx)
			_, _ = a.store.DeleteExpiredAccessTokens(ctx)
			// Registrations never approved within a day are dropped.
			_, _ = a.store.DeleteUnusedOAuthClients(ctx, time.Now().Add(-24*time.Hour).UnixMilli())
			a.limiter.cleanup()
		}
	}
}

// HashSecret is the stored form of a token, refresh token or code.
func HashSecret(secret string) string {
	return hashToken(secret)
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))

	return hex.EncodeToString(sum[:])
}

// ////////////////////////////////////////////

type attempt struct {
	failures    int
	lockedUntil time.Time
	last        time.Time
}

type limiter struct {
	mu       sync.Mutex
	max      int
	lockFor  time.Duration
	attempts map[string]*attempt
}

func newLimiter(max int, lockFor time.Duration) *limiter {
	return &limiter{max: max, lockFor: lockFor, attempts: map[string]*attempt{}}
}

func (l *limiter) locked(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	a := l.attempts[key]

	return a != nil && time.Now().Before(a.lockedUntil)
}

func (l *limiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	a := l.attempts[key]
	if a == nil {
		a = &attempt{}
		l.attempts[key] = a
	}

	a.failures++
	a.last = time.Now()

	if a.failures >= l.max {
		a.failures = 0
		a.lockedUntil = time.Now().Add(l.lockFor)
	}
}

func (l *limiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	delete(l.attempts, key)
}

func (l *limiter) cleanup() {
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := time.Now().Add(-time.Hour)
	for k, a := range l.attempts {
		if a.last.Before(cutoff) && time.Now().After(a.lockedUntil) {
			delete(l.attempts, k)
		}
	}
}

// ////////////////////////////////////////////
// access tokens

// TokenPrefix marks dataport access tokens, so secret scanners and users
// can tell them apart.
const TokenPrefix = "dpt_"

// NewAccessToken returns a new raw token and the hash to store.
func NewAccessToken() (string, string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}

	token := TokenPrefix + base64.RawURLEncoding.EncodeToString(raw)

	return token, hashToken(token), nil
}

// AuthenticateToken resolves an access token to its user. The token's
// ReadOnly flag narrows what the user may do with it.
func (a *Auth) AuthenticateToken(ctx context.Context, token string) (*store.User, *store.AccessToken, error) {
	if !strings.HasPrefix(token, TokenPrefix) {
		return nil, nil, store.ErrNotFound
	}

	t, err := a.store.GetAccessTokenByHash(ctx, hashToken(token))
	if err != nil {
		return nil, nil, err
	}

	ts := time.Now().UnixMilli()
	if t.ExpiresAt > 0 && ts > t.ExpiresAt {
		return nil, nil, store.ErrNotFound
	}

	user, err := a.store.GetUser(ctx, t.UserID)
	if err != nil {
		return nil, nil, err
	}

	if user.Disabled {
		return nil, nil, store.ErrNotFound
	}

	if ts-t.LastUsedAt > time.Minute.Milliseconds() {
		_ = a.store.TouchAccessToken(ctx, t.ID, ts)
	}

	return user, t, nil
}
