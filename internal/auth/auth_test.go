package auth

import "testing"

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
