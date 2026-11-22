// Package filestore 保存用户附件原件和元数据；磁盘路径只使用内部生成的 ID。
package filestore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"gitlab.com/elegantgx/chat-deepseek/internal/id"
)

const (
	// MaxFileSize 与前端单文件上限一致。
	MaxFileSize = 64 << 20
	// MaxUserDataSize 是 user_data 类文件的上限，与 DeepSeek Files API 一致。
	MaxUserDataSize = 32 << 20
	// MaxNameLength 是展示文件名的字节上限。
	MaxNameLength = 255
)

var (
	// ErrNotFound 表示文件不存在、已删除或不属于当前用户。
	ErrNotFound = errors.New("filestore: not found")
	// ErrTooLarge 表示文件超过对应 purpose 的上限。
	ErrTooLarge = errors.New("filestore: file too large")
	// ErrInvalidFile 表示文件名、purpose 或用户 ID 不合法。
	ErrInvalidFile = errors.New("filestore: invalid file")
)

// File 是附件元数据；ServerID 是上传到 DeepSeek 后拿到的远端 ID。
type File struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	IsImage   bool   `json:"isImage"`
	MIMEType  string `json:"mimeType"`
	Purpose   string `json:"purpose"`
	ServerID  string `json:"serverId,omitempty"`
	CreatedAt int64  `json:"createdAt"`
}

// Store 管理 data/files/<user_id>/ 下的原件与 files 表。
type Store struct {
	db   *sql.DB
	root string
	now  func() time.Time
}

// NewStore 创建附件存储；root 通常是数据目录下的 files/。
func NewStore(db *sql.DB, dataDir string) (*Store, error) {
	root := filepath.Join(dataDir, "files")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, fmt.Errorf("create files directory: %w", err)
	}
	return &Store{db: db, root: root, now: time.Now}, nil
}

// Create 把 src 写入用户目录并登记元数据；size 参数只用于上游限制判断，实际大小以拷贝结果为准。
func (s *Store) Create(
	ctx context.Context,
	userID, name, mimeType, purpose string,
	isImage bool,
	size int64,
	src io.Reader,
) (*File, error) {
	if err := safeComponent(userID); err != nil {
		return nil, err
	}
	purpose = strings.TrimSpace(purpose)
	if purpose != "" && purpose != "rich_layout_recognition" && purpose != "user_data" {
		return nil, fmt.Errorf("%w: unsupported purpose", ErrInvalidFile)
	}
	name = cleanName(name)
	mimeType = strings.TrimSpace(mimeType)
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	limit := int64(MaxFileSize)
	if purpose == "user_data" {
		limit = MaxUserDataSize
	}
	if size > limit {
		return nil, ErrTooLarge
	}

	dir := filepath.Join(s.root, userID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create user files directory: %w", err)
	}
	fileID, err := id.New()
	if err != nil {
		return nil, fmt.Errorf("generate file id: %w", err)
	}
	temp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	tempPath := temp.Name()
	defer func() {
		_ = temp.Close()
		_ = os.Remove(tempPath)
	}()
	written, err := io.Copy(temp, io.LimitReader(src, limit+1))
	if err != nil {
		return nil, fmt.Errorf("write file: %w", err)
	}
	if written > limit {
		return nil, ErrTooLarge
	}
	if written == 0 {
		return nil, fmt.Errorf("%w: empty file", ErrInvalidFile)
	}
	if err := temp.Close(); err != nil {
		return nil, fmt.Errorf("close file: %w", err)
	}
	finalPath := filepath.Join(dir, fileID)
	if err := os.Rename(tempPath, finalPath); err != nil {
		return nil, fmt.Errorf("move file: %w", err)
	}
	now := s.now().UnixMilli()
	if _, err := s.db.ExecContext(
		ctx,
		`INSERT INTO files (id, user_id, name, size, is_image, mime_type, purpose, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		fileID, userID, name, written, isImage, mimeType, purpose, now,
	); err != nil {
		_ = os.Remove(finalPath)
		return nil, fmt.Errorf("record file: %w", err)
	}
	return &File{
		ID:        fileID,
		Name:      name,
		Size:      written,
		IsImage:   isImage,
		MIMEType:  mimeType,
		Purpose:   purpose,
		CreatedAt: now,
	}, nil
}

// Open 返回附件元数据和只读文件句柄；调用方负责关闭。
func (s *Store) Open(ctx context.Context, userID, fileID string) (*File, *os.File, error) {
	file, err := s.Get(ctx, userID, fileID)
	if err != nil {
		return nil, nil, err
	}
	handle, err := os.Open(filepath.Join(s.root, userID, file.ID))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, fmt.Errorf("open file: %w", err)
	}
	return file, handle, nil
}

// Get 只读取元数据，用于代理上传前判断是否已有远端 ID。
func (s *Store) Get(ctx context.Context, userID, fileID string) (*File, error) {
	if err := safeComponent(userID); err != nil {
		return nil, err
	}
	var file File
	var isImage bool
	var serverID sql.NullString
	err := s.db.QueryRowContext(
		ctx,
		`SELECT id, name, size, is_image, mime_type, purpose, server_id, created_at
		 FROM files WHERE id = ? AND user_id = ? AND deleted_at IS NULL`,
		fileID,
		userID,
	).Scan(
		&file.ID,
		&file.Name,
		&file.Size,
		&isImage,
		&file.MIMEType,
		&file.Purpose,
		&serverID,
		&file.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get file: %w", err)
	}
	file.IsImage = isImage
	if serverID.Valid {
		file.ServerID = serverID.String
	}
	return &file, nil
}

// SetServerID 记录 DeepSeek Files API 返回的远端 ID。
func (s *Store) SetServerID(ctx context.Context, userID, fileID, serverID string) error {
	result, err := s.db.ExecContext(
		ctx,
		`UPDATE files SET server_id = ? WHERE id = ? AND user_id = ? AND deleted_at IS NULL`,
		serverID,
		fileID,
		userID,
	)
	if err != nil {
		return fmt.Errorf("set file server id: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("set file server id rows: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete 软删除元数据；原件保留，便于回收站恢复和备份。
func (s *Store) Delete(ctx context.Context, userID, fileID string) error {
	result, err := s.db.ExecContext(
		ctx,
		`UPDATE files SET deleted_at = ? WHERE id = ? AND user_id = ? AND deleted_at IS NULL`,
		s.now().UnixMilli(),
		fileID,
		userID,
	)
	if err != nil {
		return fmt.Errorf("delete file: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete file rows: %w", err)
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func safeComponent(value string) error {
	if value == "" || value == "." || value == ".." ||
		strings.ContainsAny(value, `/\`) {
		return fmt.Errorf("%w: invalid path component", ErrInvalidFile)
	}
	return nil
}

func cleanName(name string) string {
	name = path.Base(strings.ReplaceAll(strings.TrimSpace(name), `\`, "/"))
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
	if name == "" || name == "." || name == "/" {
		name = "file"
	}
	if len(name) > MaxNameLength {
		// 按字节截断可能切坏 UTF-8，这里保守地只保留完整 ASCII 前缀。
		name = strings.ToValidUTF8(name[:MaxNameLength], "")
	}
	return name
}
