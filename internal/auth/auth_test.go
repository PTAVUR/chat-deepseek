package auth

import "testing"

func TestHashAndVerifyPassword(t *testing.T) {
	hash, err := HashPassword("correct-horse-battery")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	ok, err := VerifyPassword(hash, "correct-horse-battery")
	if err != nil || !ok {
		t.Fatalf("VerifyPassword correct = %v, %v", ok, err)
	}
	ok, err = VerifyPassword(hash, "wrong-password")
	if err != nil {
		t.Fatalf("VerifyPassword wrong: %v", err)
	}
	if ok {
		t.Fatal("wrong password verified")
	}
	if _, err := VerifyPassword("not-a-hash", "x"); err == nil {
		t.Fatal("malformed hash accepted")
	}
}

func TestValidatePassword(t *testing.T) {
	if err := ValidatePassword("short"); err == nil {
		t.Fatal("short password accepted")
	}
	if err := ValidatePassword("long-enough"); err != nil {
		t.Fatalf("valid password rejected: %v", err)
	}
}

func TestSessionSecrets(t *testing.T) {
	token, hash, csrf, err := NewSessionSecrets()
	if err != nil {
		t.Fatalf("NewSessionSecrets: %v", err)
	}
	if token == "" || hash == "" || csrf == "" {
		t.Fatal("empty session secret")
	}
	if HashToken(token) != hash {
		t.Fatal("token hash mismatch")
	}
	otherToken, otherHash, _, err := NewSessionSecrets()
	if err != nil {
		t.Fatalf("NewSessionSecrets second: %v", err)
	}
	if token == otherToken || hash == otherHash {
		t.Fatal("session secrets are not unique")
	}
}
