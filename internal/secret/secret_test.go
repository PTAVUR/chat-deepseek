package secret

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestLoadOrCreateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	first, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	second, err := LoadOrCreate(dir)
	if err != nil {
		t.Fatalf("load key: %v", err)
	}

	ciphertext, nonce, version, err := first.Encrypt("sk-test-secret")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if version != KeyVersion {
		t.Fatalf("version = %d", version)
	}
	plaintext, err := second.Decrypt(ciphertext, nonce, version)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if plaintext != "sk-test-secret" {
		t.Fatalf("plaintext = %q", plaintext)
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(dir, FileName))
		if err != nil {
			t.Fatalf("stat key: %v", err)
		}
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Fatalf("key mode = %o", mode)
		}
	}
}

func TestDecryptRejectsTamperingAndWrongKey(t *testing.T) {
	first, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	other, err := LoadOrCreate(t.TempDir())
	if err != nil {
		t.Fatalf("create other key: %v", err)
	}
	ciphertext, nonce, version, err := first.Encrypt("sk-test-secret")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}

	tampered := bytes.Clone(ciphertext)
	tampered[0] ^= 0xff
	if _, err := first.Decrypt(tampered, nonce, version); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("tampered error = %v", err)
	}
	if _, err := other.Decrypt(ciphertext, nonce, version); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("wrong key error = %v", err)
	}
	if _, err := first.Decrypt(ciphertext, nonce, KeyVersion+1); !errors.Is(err, ErrDecrypt) {
		t.Fatalf("version error = %v", err)
	}
}

func TestLoadRejectsInvalidKeyFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte("short"), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	if _, err := LoadOrCreate(dir); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("error = %v", err)
	}
}
