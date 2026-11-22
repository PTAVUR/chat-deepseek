// Package chat 保存完整聊天会话 JSON，并提供按用户隔离的软删除与版本锁。
package chat

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

const (
	// MaxSessionSize 限制单个会话 JSON 的大小，避免一次请求把数据库撑爆。
	MaxSessionSize = 16 << 20
	// MaxTitleLength 是标题的字节上限。
	MaxTitleLength = 500
	// MaxIDLength 是会话 ID 的字节上限。
	MaxIDLength = 128
)

var (
	// ErrNotFound 表示会话不存在、已删除或不属于当前用户。
	ErrNotFound = errors.New("chat: not found")
	// ErrConflict 表示客户端携带的 version 已过期。
	ErrConflict = errors.New("chat: version conflict")
	// ErrInvalidSession 表示会话 JSON 缺少必要字段或格式不合法。
	ErrInvalidSession = errors.New("chat: invalid session")
)

// Summary 是会话列表需要的元数据，不包含完整消息。
type Summary struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

// Stored 是完整会话及其当前版本。
type Stored struct {
	Session json.RawMessage `json:"session"`
	Version int64           `json:"version"`
}

// Store 读写 chats 表。
type Store struct {
	db  *sql.DB
	now func() time.Time
}

// NewStore 创建会话存储。
func NewStore(db *sql.DB) *Store {
	return &Store{db: db, now: time.Now}
}

type sessionEnvelope struct {
	ID            string            `json:"id"`
	Title         string            `json:"title"`
	CreatedAt     int64             `json:"createdAt"`
	UpdatedAt     int64             `json:"updatedAt"`
	Messages      []json.RawMessage `json:"messages"`
	RootMessageID json.RawMessage   `json:"rootMessageId"`
}

func parseSession(raw []byte) (sessionEnvelope, error) {
	if len(raw) > MaxSessionSize {
		return sessionEnvelope{}, fmt.Errorf("%w: too large", ErrInvalidSession)
	}
	var session sessionEnvelope
	if err := json.Unmarshal(raw, &session); err != nil {
		return sessionEnvelope{}, fmt.Errorf("%w: %v", ErrInvalidSession, err)
	}
	if err := validateID(session.ID); err != nil {
		return sessionEnvelope{}, err
	}
	if len(session.Title) > MaxTitleLength {
		return sessionEnvelope{}, fmt.Errorf("%w: title too long", ErrInvalidSession)
	}
	if string(session.RootMessageID) != "null" && len(session.RootMessageID) > 0 {
		var root string
		if err := json.Unmarshal(session.RootMessageID, &root); err != nil {
			return sessionEnvelope{}, fmt.Errorf("%w: invalid rootMessageId", ErrInvalidSession)
		}
	}
	return session, nil
}

func validateID(id string) error {
	if id == "" || len(id) > MaxIDLength {
		return fmt.Errorf("%w: invalid id length", ErrInvalidSession)
	}
	for _, r := range id {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.' {
			continue
		}
		return fmt.Errorf("%w: invalid id character", ErrInvalidSession)
	}
	return nil
}

func rootMessageID(raw json.RawMessage) any {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil
	}
	return value
}

// Create 新建会话，版本从 1 开始；相同 ID 已存在时返回 ErrConflict。
func (s *Store) Create(ctx context.Context, userID string, raw []byte) (Stored, error) {
	session, err := parseSession(raw)
	if err != nil {
		return Stored{}, err
	}
	now := s.now().UnixMilli()
	createdAt := session.CreatedAt
	if createdAt <= 0 {
		createdAt = now
	}
	title := strings.TrimSpace(session.Title)
	if _, err := s.db.ExecContext(
		ctx,
		`INSERT INTO chats (id, user_id, title, data, version, created_at, updated_at, root_message_id)
		 VALUES (?, ?, ?, ?, 1, ?, ?, ?)`,
		session.ID,
		userID,
		title,
		raw,
		createdAt,
		now,
		rootMessageID(session.RootMessageID),
	); err != nil {
		if isUniqueViolation(err) {
			return Stored{}, ErrConflict
		}
		return Stored{}, fmt.Errorf("create chat: %w", err)
	}
	return Stored{Session: json.RawMessage(raw), Version: 1}, nil
}

// Get 读取未删除的会话。
func (s *Store) Get(ctx context.Context, userID, id string) (Stored, error) {
	var data []byte
	var version int64
	err := s.db.QueryRowContext(
		ctx,
		`SELECT data, version FROM chats
		 WHERE id = ? AND user_id = ? AND deleted_at IS NULL`,
		id,
		userID,
	).Scan(&data, &version)
	if errors.Is(err, sql.ErrNoRows) {
		return Stored{}, ErrNotFound
	}
	if err != nil {
		return Stored{}, fmt.Errorf("get chat: %w", err)
	}
	return Stored{Session: json.RawMessage(data), Version: version}, nil
}

// List 返回会话摘要；deleted 为 true 时返回回收站内容。
func (s *Store) List(ctx context.Context, userID string, deleted bool) ([]Summary, error) {
	condition := "deleted_at IS NULL"
	if deleted {
		condition = "deleted_at IS NOT NULL"
	}
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT id, title, created_at, updated_at FROM chats
		 WHERE user_id = ? AND `+condition+`
		 ORDER BY updated_at DESC`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("list chats: %w", err)
	}
	defer func() { _ = rows.Close() }()
	summaries := make([]Summary, 0)
	for rows.Next() {
		var summary Summary
		if err := rows.Scan(&summary.ID, &summary.Title, &summary.CreatedAt, &summary.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan chat: %w", err)
		}
		summaries = append(summaries, summary)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list chats: %w", err)
	}
	return summaries, nil
}

// Update 用版本锁更新会话；版本不匹配时返回 ErrConflict。
func (s *Store) Update(ctx context.Context, userID, id string, raw []byte, version int64) (Stored, error) {
	if version < 1 {
		return Stored{}, fmt.Errorf("%w: invalid version", ErrInvalidSession)
	}
	session, err := parseSession(raw)
	if err != nil {
		return Stored{}, err
	}
	if session.ID != id {
		return Stored{}, fmt.Errorf("%w: id mismatch", ErrInvalidSession)
	}
	now := s.now().UnixMilli()
	result, err := s.db.ExecContext(
		ctx,
		`UPDATE chats SET
		   title = ?, data = ?, version = version + 1, updated_at = ?, root_message_id = ?
		 WHERE id = ? AND user_id = ? AND deleted_at IS NULL AND version = ?`,
		strings.TrimSpace(session.Title),
		raw,
		now,
		rootMessageID(session.RootMessageID),
		id,
		userID,
		version,
	)
	if err != nil {
		return Stored{}, fmt.Errorf("update chat: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Stored{}, fmt.Errorf("update chat rows: %w", err)
	}
	if affected == 0 {
		var current int64
		err := s.db.QueryRowContext(
			ctx,
			`SELECT version FROM chats WHERE id = ? AND user_id = ? AND deleted_at IS NULL`,
			id,
			userID,
		).Scan(&current)
		if errors.Is(err, sql.ErrNoRows) {
			return Stored{}, ErrNotFound
		}
		if err != nil {
			return Stored{}, fmt.Errorf("check chat version: %w", err)
		}
		return Stored{}, ErrConflict
	}
	return Stored{Session: json.RawMessage(raw), Version: version + 1}, nil
}

// SoftDelete 把会话移入回收站。
func (s *Store) SoftDelete(ctx context.Context, userID, id string) error {
	result, err := s.db.ExecContext(
		ctx,
		`UPDATE chats SET deleted_at = ? WHERE id = ? AND user_id = ? AND deleted_at IS NULL`,
		s.now().UnixMilli(),
		id,
		userID,
	)
	if err != nil {
		return fmt.Errorf("delete chat: %w", err)
	}
	return requireAffected(result, "delete chat")
}

// Restore 从回收站恢复会话。
func (s *Store) Restore(ctx context.Context, userID, id string) error {
	result, err := s.db.ExecContext(
		ctx,
		`UPDATE chats SET deleted_at = NULL, updated_at = ?
		 WHERE id = ? AND user_id = ? AND deleted_at IS NOT NULL`,
		s.now().UnixMilli(),
		id,
		userID,
	)
	if err != nil {
		return fmt.Errorf("restore chat: %w", err)
	}
	return requireAffected(result, "restore chat")
}

// Purge 永久删除回收站中的会话。
func (s *Store) Purge(ctx context.Context, userID, id string) error {
	result, err := s.db.ExecContext(
		ctx,
		`DELETE FROM chats WHERE id = ? AND user_id = ? AND deleted_at IS NOT NULL`,
		id,
		userID,
	)
	if err != nil {
		return fmt.Errorf("purge chat: %w", err)
	}
	return requireAffected(result, "purge chat")
}

func requireAffected(result sql.Result, action string) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("%s rows: %w", action, err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func isUniqueViolation(err error) bool {
	// modernc.org/sqlite 的约束错误文本包含 UNIQUE constraint failed；不引入驱动内部错误类型。
	return strings.Contains(strings.ToLower(err.Error()), "unique constraint failed")
}
