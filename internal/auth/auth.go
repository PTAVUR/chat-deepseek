// Package auth 提供密码哈希、登录会话和 CSRF 支持。
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"

	"github.com/elegantgx/chat-deepseek/internal/id"
)

// MinPasswordLength 是 CLI/服务端接受的最短密码长度。
const MinPasswordLength = 8

// MaxPasswordLength 防止超长输入消耗 Argon2。
const MaxPasswordLength = 1024

// ErrInvalidPassword 表示密码为空或格式不符合要求。
var ErrInvalidPassword = errors.New("auth: invalid password")

// HashPassword 使用 Argon2id 生成带参数和盐的编码哈希。
func HashPassword(password string) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	const (
		memory  uint32 = 64 * 1024
		time    uint32 = 3
		threads uint8  = 4
		keyLen  uint32 = 32
	)
	hash := argon2.IDKey([]byte(password), salt, time, memory, threads, keyLen)
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		memory,
		time,
		threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

// ValidatePassword 检查密码长度；具体策略由调用方决定。
func ValidatePassword(password string) error {
	if len(password) < MinPasswordLength || len(password) > MaxPasswordLength {
		return ErrInvalidPassword
	}
	return nil
}

// VerifyPassword 校验明文密码与编码哈希。
func VerifyPassword(encoded, password string) (bool, error) {
	params, salt, expected, err := parseHash(encoded)
	if err != nil {
		return false, err
	}
	actual := argon2.IDKey(
		[]byte(password),
		salt,
		params.time,
		params.memory,
		params.threads,
		uint32(len(expected)),
	)
	return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}

type hashParams struct {
	memory  uint32
	time    uint32
	threads uint8
}

func parseHash(encoded string) (hashParams, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return hashParams{}, nil, nil, ErrInvalidPassword
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return hashParams{}, nil, nil, ErrInvalidPassword
	}
	var params hashParams
	if _, err := fmt.Sscanf(
		parts[3],
		"m=%d,t=%d,p=%d",
		&params.memory,
		&params.time,
		&params.threads,
	); err != nil {
		return hashParams{}, nil, nil, ErrInvalidPassword
	}
	if params.memory == 0 || params.time == 0 || params.threads == 0 {
		return hashParams{}, nil, nil, ErrInvalidPassword
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) < 8 {
		return hashParams{}, nil, nil, ErrInvalidPassword
	}
	hash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(hash) == 0 {
		return hashParams{}, nil, nil, ErrInvalidPassword
	}
	return params, salt, hash, nil
}

var (
	dummyOnce sync.Once
	dummyHash string
)

// VerifyDummy 在用户名不存在时消耗与真实校验接近的时间，降低枚举风险。
func VerifyDummy(password string) {
	dummyOnce.Do(func() {
		dummyHash, _ = HashPassword("dummy-password-for-timing")
	})
	_, _ = VerifyPassword(dummyHash, password)
}

// Session 是服务端登录会话；原始 token 只存在于浏览器 Cookie 或 CLI 会话文件里。
type Session struct {
	ID        string
	UserID    string
	CSRF      string
	Scope     SessionScope
	CreatedAt int64
	ExpiresAt int64
}

// SessionScope 区分浏览器会话和 CLI 会话，防止两种 token 互相冒用。
type SessionScope string

const (
	// ScopeWeb 是浏览器登录会话。
	ScopeWeb SessionScope = "web"
	// ScopeCLI 是命令行登录会话。
	ScopeCLI SessionScope = "cli"
)

// Valid 判断 scope 是否是已知取值。
func (s SessionScope) Valid() bool {
	return s == ScopeWeb || s == ScopeCLI
}

const (
	// CookieName 是会话 Cookie 名。
	CookieName = "chat_deepseek_session"
	// SessionTTL 是浏览器会话有效期。
	SessionTTL = 30 * 24 * time.Hour
)

// NewSessionSecrets 生成原始 token、数据库里存储的 token 哈希和 CSRF token。
func NewSessionSecrets() (token string, tokenHash string, csrf string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", "", fmt.Errorf("generate session token: %w", err)
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	csrfRaw := make([]byte, 32)
	if _, err := rand.Read(csrfRaw); err != nil {
		return "", "", "", fmt.Errorf("generate csrf token: %w", err)
	}
	return token, HashToken(token), base64.RawURLEncoding.EncodeToString(csrfRaw), nil
}

// HashToken 返回 token 的 SHA-256 十六进制值。
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// ErrSessionNotFound 表示会话不存在或已过期删除。
var ErrSessionNotFound = errors.New("auth: session not found")

// SessionStore 管理 auth_sessions 表。
type SessionStore struct {
	db *sql.DB
}

// NewSessionStore 创建会话存储。
func NewSessionStore(db *sql.DB) *SessionStore {
	return &SessionStore{db: db}
}

// SessionParams 是创建登录会话所需的字段；TTL 由调用方决定。
type SessionParams struct {
	UserID    string
	TokenHash string
	CSRF      string
	Scope     SessionScope
	UserAgent string
	IP        string
	TTL       time.Duration
}

// Create 保存会话并返回记录。
func (s *SessionStore) Create(
	ctx context.Context,
	params SessionParams,
	now time.Time,
) (*Session, error) {
	if !params.Scope.Valid() {
		return nil, fmt.Errorf("invalid session scope %q", params.Scope)
	}
	if params.TTL <= 0 {
		return nil, errors.New("auth: session ttl must be positive")
	}
	sessionID, err := id.New()
	if err != nil {
		return nil, fmt.Errorf("generate session id: %w", err)
	}
	created := now.UnixMilli()
	expires := now.Add(params.TTL).UnixMilli()
	userAgent := truncate(params.UserAgent, 255)
	ip := truncate(params.IP, 64)
	if _, err := s.db.ExecContext(
		ctx,
		`INSERT INTO auth_sessions
		   (id, user_id, token_hash, csrf_token, scope, created_at, last_seen_at, expires_at, user_agent, ip)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sessionID,
		params.UserID,
		params.TokenHash,
		params.CSRF,
		string(params.Scope),
		created,
		created,
		expires,
		userAgent,
		ip,
	); err != nil {
		return nil, fmt.Errorf("insert session: %w", err)
	}
	return &Session{
		ID:        sessionID,
		UserID:    params.UserID,
		CSRF:      params.CSRF,
		Scope:     params.Scope,
		CreatedAt: created,
		ExpiresAt: expires,
	}, nil
}

// GetByTokenHash 按 token 哈希和用途读取会话。
func (s *SessionStore) GetByTokenHash(
	ctx context.Context,
	scope SessionScope,
	tokenHash string,
) (*Session, error) {
	var session Session
	var storedScope string
	err := s.db.QueryRowContext(
		ctx,
		`SELECT id, user_id, csrf_token, scope, created_at, expires_at
		   FROM auth_sessions WHERE token_hash = ? AND scope = ?`,
		tokenHash,
		string(scope),
	).Scan(
		&session.ID,
		&session.UserID,
		&session.CSRF,
		&storedScope,
		&session.CreatedAt,
		&session.ExpiresAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSessionNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query session: %w", err)
	}
	session.Scope = SessionScope(storedScope)
	return &session, nil
}

// Touch 更新最近活跃时间；失败不影响请求。
func (s *SessionStore) Touch(ctx context.Context, sessionID string, now time.Time) {
	_, _ = s.db.ExecContext(
		ctx,
		`UPDATE auth_sessions SET last_seen_at = ? WHERE id = ?`,
		now.UnixMilli(),
		sessionID,
	)
}

// Delete 删除单个会话。
func (s *SessionStore) Delete(ctx context.Context, sessionID string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM auth_sessions WHERE id = ?`, sessionID); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// DeleteByUser 删除账户的全部会话。
func (s *SessionStore) DeleteByUser(ctx context.Context, userID string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM auth_sessions WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("delete user sessions: %w", err)
	}
	return nil
}

// DeleteExpired 清理过期会话。
func (s *SessionStore) DeleteExpired(ctx context.Context, before time.Time) (int64, error) {
	result, err := s.db.ExecContext(ctx, `DELETE FROM auth_sessions WHERE expires_at <= ?`, before.UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("delete expired sessions: %w", err)
	}
	return result.RowsAffected()
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}
