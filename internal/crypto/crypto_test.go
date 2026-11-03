package crypto

import (
	"strings"
	"testing"
	"time"
)

func TestRoundTrip(t *testing.T) {
	c, err := New("key")
	if err != nil {
		t.Fatal(err)
	}

	enc, err := c.Encrypt("s3cret")
	if err != nil {
		t.Fatal(err)
	}

	if enc == "s3cret" {
		t.Fatal("not encrypted")
	}

	got, err := c.Decrypt(enc)
	if err != nil || got != "s3cret" {
		t.Fatalf("Decrypt = %q, %v", got, err)
	}

	other, _ := New("other")
	if _, err := other.Decrypt(enc); err == nil {
		t.Fatal("decrypted with the wrong key")
	}
}

func TestAnyKey(t *testing.T) {
	for _, key := range []string{"x", "test", "çok gizli bir cümle 🔑", strings.Repeat("a", 500)} {
		c, err := New(key)
		if err != nil {
			t.Fatalf("%q: %v", key, err)
		}

		enc, _ := c.Encrypt("pw")
		if got, err := c.Decrypt(enc); err != nil || got != "pw" {
			t.Fatalf("%q round trip: %q %v", key, got, err)
		}
	}

	// An empty key is a key like any other, and differs from a set one.
	empty, err := New("")
	if err != nil {
		t.Fatal(err)
	}

	enc, _ := empty.Encrypt("pw")
	if got, err := empty.Decrypt(enc); err != nil || got != "pw" {
		t.Fatalf("empty key round trip: %q %v", got, err)
	}

	if other, _ := New("x"); func() bool { _, err := other.Decrypt(enc); return err == nil }() {
		t.Fatal("decrypted with the wrong key")
	}
}

func TestRotate(t *testing.T) {
	c, _ := New("old")
	before, _ := c.Encrypt("pw")

	if err := c.Rotate("new"); err != nil {
		t.Fatal(err)
	}

	if !c.Is("new") || c.Is("old") {
		t.Fatal("current key")
	}

	// Values sealed with the key before stay readable until sealed again.
	if got, err := c.Decrypt(before); err != nil || got != "pw" || c.Current(before) {
		t.Fatalf("old value: %q %v", got, err)
	}

	after, _ := c.Encrypt("pw")
	if !c.Current(after) {
		t.Fatal("new value not under the new key")
	}

	if fresh, _ := New("new"); func() bool { _, err := fresh.Decrypt(before); return err == nil }() {
		t.Fatal("a cipher of only the new key read the old value")
	}
}

// Keys before several rotations stay readable, and encryption waits while
// a rotation runs.
func TestRotateTwice(t *testing.T) {
	c, _ := New("a")
	first, _ := c.Encrypt("pw")
	_ = c.Rotate("b")
	second, _ := c.Encrypt("pw")
	_ = c.Rotate("c")

	for _, v := range []string{first, second} {
		if got, err := c.Decrypt(v); err != nil || got != "pw" {
			t.Fatalf("value before two rotations: %q %v", got, err)
		}
	}

	done := c.Rotating()
	sealed := make(chan struct{})
	go func() { _, _ = c.Encrypt("x"); close(sealed) }()
	select {
	case <-sealed:
		t.Fatal("encrypted during a rotation")
	case <-time.After(50 * time.Millisecond):
	}
	done()
	<-sealed
}
