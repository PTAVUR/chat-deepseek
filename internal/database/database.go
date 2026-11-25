// Package database 打开 SQLite 并执行 schema 迁移。
package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// schemaVersion 是当前数据库结构版本；每次改表都必须递增。
const schemaVersion = 5

// Open 打开数据目录下的 chat.db，启用 WAL/外键并执行迁移。
func Open(dataDir string) (*sql.DB, error) {
	// 先转成绝对路径：SQLite 的 file URI 无法表达相对路径（会被当成
	// authority），而且绝对路径在错误信息和日志里更明确。
	dir, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, fmt.Errorf("resolve data directory: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	path := filepath.Join(dir, "chat.db")
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	// 单连接避免 SQLite 写锁竞争；聊天服务并发量不大，先以正确性优先。
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	if err := migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	_ = os.Chmod(path, 0o600)
	return db, nil
}

// Initialized 只读检查数据目录里是否存在活动 Owner。
// 数据库不存在时返回 false，且不会创建数据库、WAL 或执行迁移。
func Initialized(dataDir string) (bool, error) {
	dir, err := filepath.Abs(dataDir)
	if err != nil {
		return false, fmt.Errorf("resolve data directory: %w", err)
	}
	path := filepath.Join(dir, "chat.db")
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("检查数据库文件: %w", err)
	}
	db, err := sql.Open("sqlite", readOnlyDSN(path))
	if err != nil {
		return false, fmt.Errorf("open database: %w", err)
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var count int
	err = db.QueryRowContext(ctx, `SELECT COUNT(*)
		FROM user_groups ug
		JOIN users u ON u.id = ug.user_id
		WHERE ug.group_name = 'Owner' AND u.disabled = 0`).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("读取数据库: %w", err)
	}
	return count > 0, nil
}

// readOnlyDSN 生成只读 DSN。没有 chat.db-wal 时用 immutable=1：
// SQLite 读 WAL 模式的库会创建 -shm/-wal，而 backup 命令看到它们就会
// 拒绝执行；immutable 告诉 SQLite 文件不会被并发修改，从而完全不写盘。
// 服务运行时 -wal 存在，此时只能普通只读打开（backup 本来也会拒绝）。
func readOnlyDSN(path string) string {
	u := &url.URL{Scheme: "file", Path: uriPath(path)}
	query := u.Query()
	query.Set("mode", "ro")
	if _, err := os.Stat(path + "-wal"); errors.Is(err, os.ErrNotExist) {
		query.Set("immutable", "1")
	}
	query.Add("_pragma", "busy_timeout(5000)")
	u.RawQuery = query.Encode()
	return u.String()
}

func dsn(path string) string {
	u := &url.URL{Scheme: "file", Path: uriPath(path)}
	query := u.Query()
	query.Add("_pragma", "busy_timeout(5000)")
	query.Add("_pragma", "journal_mode(WAL)")
	query.Add("_pragma", "foreign_keys(1)")
	query.Add("_pragma", "synchronous(NORMAL)")
	u.RawQuery = query.Encode()
	return u.String()
}

// uriPath 把绝对文件路径转换成 file URI 的 path 部分。
// Windows 盘符路径形如 C:/Users/...，如果直接作为 URL.Path，String()
// 会生成 file://C:/Users/...，SQLite 因此把 "C:" 当成 URI 的 authority
// 并报 "invalid uri authority: C:"。URI 的 path 必须以 "/" 开头，
// 补成 /C:/Users/... 后才是合法的 file:///C:/Users/...。
// UNC 路径 //server/share/... 本身已带前导斜杠，保持原样交给 SQLite。
func uriPath(path string) string {
	slashPath := filepath.ToSlash(path)
	if len(slashPath) >= 3 &&
		((slashPath[0] >= 'a' && slashPath[0] <= 'z') || (slashPath[0] >= 'A' && slashPath[0] <= 'Z')) &&
		slashPath[1] == ':' && slashPath[2] == '/' {
		return "/" + slashPath
	}
	return slashPath
}

func migrate(ctx context.Context, db *sql.DB) error {
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	if version > schemaVersion {
		return fmt.Errorf("database schema version %d is newer than supported %d", version, schemaVersion)
	}
	for _, migration := range migrations {
		if migration.version <= version {
			continue
		}
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin schema migration %d: %w", migration.version, err)
		}
		if _, err := tx.ExecContext(ctx, migration.sql); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply schema %d: %w", migration.version, err)
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", migration.version)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("set schema version %d: %w", migration.version, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit schema migration %d: %w", migration.version, err)
		}
		version = migration.version
	}
	return nil
}

type migration struct {
	version int
	sql     string
}

var migrations = []migration{
	{version: 1, sql: schemaV1},
	{version: 2, sql: schemaV2},
	{version: 3, sql: schemaV3},
	{version: 4, sql: schemaV4},
	{version: 5, sql: schemaV5},
}

const schemaV1 = `
CREATE TABLE groups (
  name TEXT PRIMARY KEY,
  rank INTEGER NOT NULL UNIQUE CHECK (rank >= 1)
);

INSERT INTO groups (name, rank) VALUES
  ('User', 1),
  ('Admin', 2),
  ('Owner', 3);

CREATE TABLE users (
  id TEXT PRIMARY KEY,
  username TEXT NOT NULL UNIQUE COLLATE NOCASE,
  password_hash TEXT NOT NULL,
  disabled INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE user_groups (
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  group_name TEXT NOT NULL REFERENCES groups(name) ON DELETE RESTRICT,
  PRIMARY KEY (user_id, group_name)
);

CREATE INDEX idx_user_groups_group ON user_groups(group_name);

CREATE TABLE auth_sessions (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash TEXT NOT NULL UNIQUE,
  csrf_token TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  last_seen_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  user_agent TEXT NOT NULL DEFAULT '',
  ip TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_auth_sessions_user ON auth_sessions(user_id);
CREATE INDEX idx_auth_sessions_expires ON auth_sessions(expires_at);

CREATE TABLE chats (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  title TEXT NOT NULL DEFAULT '',
  data TEXT NOT NULL,
  version INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  deleted_at INTEGER,
  root_message_id TEXT
);

CREATE INDEX idx_chats_user_updated ON chats(user_id, updated_at DESC);

CREATE TABLE files (
  id TEXT PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  size INTEGER NOT NULL,
  is_image INTEGER NOT NULL DEFAULT 0,
  mime_type TEXT NOT NULL DEFAULT '',
  server_id TEXT,
  created_at INTEGER NOT NULL,
  deleted_at INTEGER
);

CREATE INDEX idx_files_user ON files(user_id);

CREATE TABLE user_settings (
  user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  preferences TEXT NOT NULL DEFAULT '{}',
  api_key_ciphertext BLOB,
  api_key_nonce BLOB,
  api_key_key_version INTEGER NOT NULL DEFAULT 1,
  updated_at INTEGER NOT NULL
);

CREATE TABLE generation_leases (
  chat_id TEXT PRIMARY KEY REFERENCES chats(id) ON DELETE CASCADE,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token TEXT NOT NULL,
  expires_at INTEGER NOT NULL
);

CREATE INDEX idx_generation_leases_expires ON generation_leases(expires_at);
`

// schemaV2 记录附件上传到 DeepSeek Files API 时使用的 purpose。
const schemaV2 = `
ALTER TABLE files ADD COLUMN purpose TEXT NOT NULL DEFAULT '';
CREATE INDEX idx_files_user_created ON files(user_id, created_at DESC);
`

// schemaV3 保存用户头像。头像是前端压缩后的 data URL，空字符串表示使用用户名首字母。
const schemaV3 = `
ALTER TABLE users ADD COLUMN avatar TEXT NOT NULL DEFAULT '';
`

// schemaV4 移除回收站和未启用的生成租约：删除是永久的，不再保留 deleted_at。
const schemaV4 = `
DELETE FROM chats WHERE deleted_at IS NOT NULL;
DELETE FROM files WHERE deleted_at IS NOT NULL;
DROP TABLE generation_leases;
ALTER TABLE chats DROP COLUMN deleted_at;
ALTER TABLE files DROP COLUMN deleted_at;
`

// schemaV5 区分浏览器会话与 CLI 会话：两种 token 不能互相冒用。
// 旧会话默认按浏览器会话处理，升级后已登录的 Cookie 仍然有效。
const schemaV5 = `
ALTER TABLE auth_sessions ADD COLUMN scope TEXT NOT NULL DEFAULT 'web';
CREATE INDEX idx_auth_sessions_scope_expires ON auth_sessions(scope, expires_at);
`

// ErrNotFound 用于调用方区分“没有数据”和真实错误。
var ErrNotFound = errors.New("database: not found")
