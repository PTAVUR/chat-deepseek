// Package secret 管理数据目录里的主密钥，并负责 API Key 的对称加密。
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	// FileName 是主密钥文件名，与 SQLite 数据库分开保存。
	FileName = "master.key"
	// KeySize 是 AES-256 的密钥长度。
	KeySize = 32
	// KeyVersion 是当前密钥与密文的格式版本。
	KeyVersion = 1
)

var (
	// ErrInvalidKey 表示主密钥文件长度不正确。
	ErrInvalidKey = errors.New("secret: invalid master key")
	// ErrDecrypt 表示密文无法通过认证或格式不支持。
	ErrDecrypt = errors.New("secret: cannot decrypt value")
)

// Key 持有内存中的主密钥；调用方不要把它写入数据库或日志。
type Key struct {
	raw []byte
}

// LoadOrCreate 读取主密钥；不存在时生成 32 字节随机密钥并以 0600 写入。
func LoadOrCreate(dataDir string) (*Key, error) {
	path := filepath.Join(dataDir, FileName)
	raw, err := os.ReadFile(path)
	if err == nil {
		return newKey(raw, path)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read master key: %w", err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	raw = make([]byte, KeySize)
	if _, err := rand.Read(raw); err != nil {
		return nil, fmt.Errorf("generate master key: %w", err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		// 另一个进程刚创建了密钥，按正常路径读取。
		raw, err = os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read master key: %w", err)
		}
		return newKey(raw, path)
	}
	if err != nil {
		return nil, fmt.Errorf("create master key: %w", err)
	}
	if _, err := file.Write(raw); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return nil, fmt.Errorf("write master key: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return nil, fmt.Errorf("close master key: %w", err)
	}
	key, err := newKey(raw, path)
	if err != nil {
		_ = os.Remove(path)
		return nil, err
	}
	return key, nil
}

func newKey(raw []byte, path string) (*Key, error) {
	if len(raw) != KeySize {
		return nil, fmt.Errorf("%w: %s 长度为 %d，应为 %d", ErrInvalidKey, path, len(raw), KeySize)
	}
	// 避免密钥文件被同组或其他用户读取；Windows 上 Chmod 语义有限，失败不阻塞。
	_ = os.Chmod(path, 0o600)
	key := make([]byte, KeySize)
	copy(key, raw)
	return &Key{raw: key}, nil
}

// Encrypt 用 AES-256-GCM 加密明文，返回密文、随机 nonce 和密钥版本。
func (k *Key) Encrypt(plaintext string) (ciphertext, nonce []byte, version int, err error) {
	aead, err := k.aead()
	if err != nil {
		return nil, nil, 0, err
	}
	nonce = make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, 0, fmt.Errorf("generate nonce: %w", err)
	}
	ciphertext = aead.Seal(nil, nonce, []byte(plaintext), nil)
	return ciphertext, nonce, KeyVersion, nil
}

// Decrypt 校验并解密密文；版本不支持或认证失败时返回 ErrDecrypt。
func (k *Key) Decrypt(ciphertext, nonce []byte, version int) (string, error) {
	if version != KeyVersion {
		return "", fmt.Errorf("%w: unsupported key version %d", ErrDecrypt, version)
	}
	aead, err := k.aead()
	if err != nil {
		return "", err
	}
	if len(nonce) != aead.NonceSize() {
		return "", fmt.Errorf("%w: invalid nonce length", ErrDecrypt)
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrDecrypt, err)
	}
	return string(plaintext), nil
}

func (k *Key) aead() (cipher.AEAD, error) {
	if k == nil || len(k.raw) != KeySize {
		return nil, ErrInvalidKey
	}
	block, err := aes.NewCipher(k.raw)
	if err != nil {
		return nil, fmt.Errorf("create cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create GCM: %w", err)
	}
	return aead, nil
}
