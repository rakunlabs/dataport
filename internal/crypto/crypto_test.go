package crypto

import (
	"strings"
	"testing"
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
