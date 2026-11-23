package auth

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Attempts count as they start: many sent at once let through no more than
// the limit, and the key is then locked.
func TestLimiterParallel(t *testing.T) {
	l := newLimiter(5, time.Minute)

	var allowed atomic.Int32
	var wg sync.WaitGroup
	for range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if l.try("k", l.max) {
				allowed.Add(1)
			}
		}()
	}
	wg.Wait()

	if n := allowed.Load(); n != 5 {
		t.Fatalf("let through %d of 50 parallel attempts, want 5", n)
	}

	if l.try("k", l.max) {
		t.Fatal("a locked key took another attempt")
	}

	// A success resets the key.
	l.reset("k")
	if !l.try("k", l.max) {
		t.Fatal("after a reset")
	}
}

// Tries older than the window are forgotten.
func TestLimiterWindow(t *testing.T) {
	l := newLimiter(2, time.Minute)
	l.try("k", 2)
	l.try("k", 2)
	l.attempts["k"].start = time.Now().Add(-limitWindow - time.Second)

	if !l.try("k", 2) {
		t.Fatal("old tries still counted")
	}
}

// A stored hash asking for more memory than the server uses is refused
// without being computed.
func TestVerifyRefusesHeavyHash(t *testing.T) {
	if VerifyPassword("x", "$argon2id$v=19$m=4194304,t=2,p=2$c2FsdHNhbHRzYWx0$a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2U") {
		t.Fatal("verified")
	}
}
