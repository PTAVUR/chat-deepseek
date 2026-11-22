// Package settings 保存每个用户的界面偏好和加密后的 API Key。
package settings

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gitlab.com/elegantgx/chat-deepseek/internal/secret"
)

const (
	// MaxPreferencesSize 限制单用户偏好 JSON 的大小，避免把大对象塞进设置表。
	MaxPreferencesSize = 64 << 10
	// MaxAPIKeyLength 限制 API Key 长度；DeepSeek 的 key 远小于这个值。
	MaxAPIKeyLength = 1024
)

var (
	// ErrNoAPIKey 表示用户还没有保存 API Key。
	ErrNoAPIKey = errors.New("settings: api key not configured")
	// ErrInvalidPreferences 表示偏好不是合法的 JSON 对象，或包含明文密钥字段。
	ErrInvalidPreferences = errors.New("settings: invalid preferences")
	// ErrInvalidAPIKey 表示 API Key 为空或过长。
	ErrInvalidAPIKey = errors.New("settings: invalid api key")
)

// Settings 是返回给 API 层的设置快照，不包含 API Key 明文或密文。
type Settings struct {
	Preferences json.RawMessage
	HasAPIKey   bool
	UpdatedAt   int64
}

// Store 读写 user_settings；API Key 使用主密钥加密后保存。
type Store struct {
	db  *sql.DB
	key *secret.Key
	now func() time.Time
}

// NewStore 创建设置存储。key 不能为空，否则设置 API 无法工作。
func NewStore(db *sql.DB, key *secret.Key) *Store {
	return &Store{db: db, key: key, now: time.Now}
}

// Get 返回偏好与 API Key 是否存在；没有记录时返回空偏好。
func (s *Store) Get(ctx context.Context, userID string) (Settings, error) {
	var preferences []byte
	var hasKey bool
	var updatedAt int64
	err := s.db.QueryRowContext(
		ctx,
		`SELECT preferences, api_key_ciphertext IS NOT NULL, updated_at
		 FROM user_settings WHERE user_id = ?`,
		userID,
	).Scan(&preferences, &hasKey, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Settings{Preferences: json.RawMessage(`{}`)}, nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("read settings: %w", err)
	}
	normalized, err := NormalizePreferences(preferences)
	if err != nil {
		return Settings{}, fmt.Errorf("stored preferences: %w", err)
	}
	return Settings{Preferences: normalized, HasAPIKey: hasKey, UpdatedAt: updatedAt}, nil
}

// SavePreferences 整体替换偏好，不影响已保存的 API Key。
func (s *Store) SavePreferences(ctx context.Context, userID string, raw []byte) (Settings, error) {
	preferences, err := NormalizePreferences(raw)
	if err != nil {
		return Settings{}, err
	}
	now := s.now().UnixMilli()
	if _, err := s.db.ExecContext(
		ctx,
		`INSERT INTO user_settings (user_id, preferences, updated_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(user_id) DO UPDATE SET
		   preferences = excluded.preferences,
		   updated_at = excluded.updated_at`,
		userID,
		preferences,
		now,
	); err != nil {
		return Settings{}, fmt.Errorf("save preferences: %w", err)
	}
	return s.Get(ctx, userID)
}

// SetAPIKey 加密保存 API Key；传空字符串表示清除。偏好保持不变。
func (s *Store) SetAPIKey(ctx context.Context, userID, apiKey string) (bool, error) {
	apiKey = strings.TrimSpace(apiKey)
	now := s.now().UnixMilli()
	if apiKey == "" {
		if _, err := s.db.ExecContext(
			ctx,
			`INSERT INTO user_settings (user_id, preferences, updated_at)
			 VALUES (?, '{}', ?)
			 ON CONFLICT(user_id) DO UPDATE SET
			   api_key_ciphertext = NULL,
			   api_key_nonce = NULL,
			   api_key_key_version = ?,
			   updated_at = excluded.updated_at`,
			userID,
			now,
			secret.KeyVersion,
		); err != nil {
			return false, fmt.Errorf("clear api key: %w", err)
		}
		return false, nil
	}
	if len(apiKey) > MaxAPIKeyLength {
		return false, ErrInvalidAPIKey
	}
	ciphertext, nonce, version, err := s.key.Encrypt(apiKey)
	if err != nil {
		return false, fmt.Errorf("encrypt api key: %w", err)
	}
	if _, err := s.db.ExecContext(
		ctx,
		`INSERT INTO user_settings (
		   user_id, preferences, api_key_ciphertext, api_key_nonce, api_key_key_version, updated_at
		 ) VALUES (?, '{}', ?, ?, ?, ?)
		 ON CONFLICT(user_id) DO UPDATE SET
		   api_key_ciphertext = excluded.api_key_ciphertext,
		   api_key_nonce = excluded.api_key_nonce,
		   api_key_key_version = excluded.api_key_key_version,
		   updated_at = excluded.updated_at`,
		userID,
		ciphertext,
		nonce,
		version,
		now,
	); err != nil {
		return false, fmt.Errorf("save api key: %w", err)
	}
	return true, nil
}

// APIKey 解密并返回 API Key；未配置时返回 ErrNoAPIKey。调用方不得把它写日志或响应给前端。
func (s *Store) APIKey(ctx context.Context, userID string) (string, error) {
	var ciphertext, nonce []byte
	var version int
	err := s.db.QueryRowContext(
		ctx,
		`SELECT api_key_ciphertext, api_key_nonce, api_key_key_version
		 FROM user_settings WHERE user_id = ?`,
		userID,
	).Scan(&ciphertext, &nonce, &version)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && len(ciphertext) == 0) {
		return "", ErrNoAPIKey
	}
	if err != nil {
		return "", fmt.Errorf("read api key: %w", err)
	}
	plaintext, err := s.key.Decrypt(ciphertext, nonce, version)
	if err != nil {
		return "", fmt.Errorf("decrypt api key: %w", err)
	}
	return plaintext, nil
}

// HasAPIKey 只判断是否配置，不读取密文。
func (s *Store) HasAPIKey(ctx context.Context, userID string) (bool, error) {
	var hasKey bool
	err := s.db.QueryRowContext(
		ctx,
		`SELECT COALESCE(api_key_ciphertext IS NOT NULL, 0) FROM user_settings WHERE user_id = ?`,
		userID,
	).Scan(&hasKey)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read api key status: %w", err)
	}
	return hasKey, nil
}

// NormalizePreferences 校验并返回紧凑的偏好 JSON 对象；空值归一为 {}。
func NormalizePreferences(raw []byte) (json.RawMessage, error) {
	if len(raw) > MaxPreferencesSize {
		return nil, fmt.Errorf("%w: too large", ErrInvalidPreferences)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return json.RawMessage(`{}`), nil
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPreferences, err)
	}
	if object == nil {
		return nil, fmt.Errorf("%w: expected object", ErrInvalidPreferences)
	}
	for key := range object {
		switch strings.ToLower(key) {
		case "apikey", "api_key":
			return nil, fmt.Errorf("%w: api key must not be stored in preferences", ErrInvalidPreferences)
		}
	}
	compact, err := json.Marshal(object)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidPreferences, err)
	}
	if len(compact) > MaxPreferencesSize {
		return nil, fmt.Errorf("%w: too large", ErrInvalidPreferences)
	}
	return compact, nil
}
