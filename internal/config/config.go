// Package config 负责便携式数据目录和服务器配置。
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Config 是非敏感的服务端配置；API Key 等秘密数据存在数据库或单独文件里。
type Config struct {
	Listen    string `json:"listen"`
	PublicURL string `json:"public_url,omitempty"`
	// APIOrigin 是 DeepSeek API 的源地址，部署在代理后面时可以改。
	APIOrigin string `json:"api_origin,omitempty"`
	// BackupDir 是备份输出目录；空表示数据目录下的 backups/。
	BackupDir string `json:"backup_dir,omitempty"`
}

// Default 返回默认配置：只监听本机，使用非特权端口。
func Default() Config {
	return Config{
		Listen:    "127.0.0.1:8090",
		APIOrigin: "https://api.deepseek.com",
	}
}

// ResolveDataDir 返回数据目录。默认与二进制同级下的 data/，可通过参数覆盖。
func ResolveDataDir(explicit string) (string, error) {
	if explicit != "" {
		return filepath.Abs(explicit)
	}
	if value := os.Getenv("CHAT_DEEPSEEK_DATA_DIR"); value != "" {
		return filepath.Abs(value)
	}
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("resolve executable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	return filepath.Join(filepath.Dir(executable), "data"), nil
}

// EnsureDataDir 创建数据目录并收紧权限。
func EnsureDataDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create data directory: %w", err)
	}
	// Windows 上 Chmod 语义有限，失败不阻塞。
	_ = os.Chmod(dir, 0o700)
	return nil
}

// Load 读取数据目录下的 config.json；文件不存在时返回默认值。
func Load(dir string) (Config, error) {
	cfg := Default()
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	if cfg.Listen == "" {
		cfg.Listen = Default().Listen
	}
	if cfg.APIOrigin == "" {
		cfg.APIOrigin = Default().APIOrigin
	}
	return cfg, nil
}

// Save 原子写入 config.json，并保留文件里无法识别的字段。
func Save(dir string, cfg Config) error {
	fields, err := readFields(dir)
	if err != nil {
		return err
	}
	setField(fields, "listen", cfg.Listen)
	setField(fields, "public_url", cfg.PublicURL)
	setField(fields, "api_origin", cfg.APIOrigin)
	setField(fields, "backup_dir", cfg.BackupDir)
	return writeFields(dir, fields)
}

// Set 校验并写入单个配置项，保留文件里的其他字段（包括未知字段）。
// 空值对 public_url/backup_dir 表示清除；listen/api_origin 不允许为空。
func Set(dir, key, value string) (string, error) {
	normalized, err := normalize(key, value)
	if err != nil {
		return "", err
	}
	fields, err := readFields(dir)
	if err != nil {
		return "", err
	}
	setField(fields, key, normalized)
	if err := writeFields(dir, fields); err != nil {
		return "", err
	}
	return normalized, nil
}

// InvalidError 表示配置项或值不合法；CLI 会把它当作用法错误处理。
type InvalidError struct {
	err error
}

func (e *InvalidError) Error() string {
	return e.err.Error()
}

func (e *InvalidError) Unwrap() error {
	return e.err
}

func invalidf(format string, args ...any) error {
	return &InvalidError{err: fmt.Errorf(format, args...)}
}

// normalize 校验并规范化单个配置项。
func normalize(key, value string) (string, error) {
	value = strings.TrimSpace(value)
	switch key {
	case "listen":
		if value == "" {
			return "", invalidf("listen 不能为空")
		}
		if _, _, err := net.SplitHostPort(value); err != nil {
			return "", invalidf("listen 需要 host:port 格式: %v", err)
		}
		return value, nil
	case "public_url":
		if value == "" {
			return "", nil
		}
		normalized, err := normalizeURL("public_url", value)
		if err != nil {
			return "", &InvalidError{err: err}
		}
		return normalized, nil
	case "api_origin":
		if value == "" {
			return "", invalidf("api_origin 不能为空")
		}
		normalized, err := normalizeURL("api_origin", value)
		if err != nil {
			return "", &InvalidError{err: err}
		}
		return normalized, nil
	case "backup_dir":
		if value == "" {
			return "", nil
		}
		if !filepath.IsAbs(value) {
			return "", invalidf("backup_dir 必须是绝对路径")
		}
		return filepath.Clean(value), nil
	default:
		return "", invalidf("未知配置项 %q；可用：listen、public_url、api_origin、backup_dir", key)
	}
}

func normalizeURL(name, value string) (string, error) {
	parsed, err := url.Parse(value)
	if err != nil {
		return "", fmt.Errorf("%s 不是合法 URL: %w", name, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("%s 必须以 http:// 或 https:// 开头", name)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("%s 缺少主机名", name)
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("%s 不能带查询参数或片段", name)
	}
	return strings.TrimRight(value, "/"), nil
}

// ResolveBackupDir 返回备份目录的绝对路径：空值用数据目录下的 backups/；
// 手工写入的相对路径按数据目录解析，避免依赖当前工作目录。
func ResolveBackupDir(dataDir, configured string) (string, error) {
	dir, err := filepath.Abs(dataDir)
	if err != nil {
		return "", fmt.Errorf("resolve data directory: %w", err)
	}
	configured = strings.TrimSpace(configured)
	if configured == "" {
		return filepath.Join(dir, "backups"), nil
	}
	if !filepath.IsAbs(configured) {
		return filepath.Join(dir, configured), nil
	}
	return filepath.Clean(configured), nil
}

// readFields 读取 config.json 的原始字段；文件不存在时返回空 map。
func readFields(dir string) (map[string]json.RawMessage, error) {
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]json.RawMessage{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if fields == nil {
		fields = map[string]json.RawMessage{}
	}
	return fields, nil
}

// setField 写入字符串字段；空字符串表示删除，让 Load 回落到默认值。
func setField(fields map[string]json.RawMessage, key, value string) {
	if value == "" {
		delete(fields, key)
		return
	}
	data, err := json.Marshal(value)
	if err != nil {
		// 字符串编码不会失败；保持 map 原样，交由上层调用处理。
		return
	}
	fields[key] = data
}

// writeFields 原子写入 config.json：先写同目录临时文件，再 rename。
func writeFields(dir string, fields map[string]json.RawMessage) error {
	if err := EnsureDataDir(dir); err != nil {
		return err
	}
	data, err := json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}()
	if err := tmp.Chmod(0o600); err != nil && !errors.Is(err, os.ErrPermission) {
		return fmt.Errorf("set config permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	if err := os.Rename(tmpPath, filepath.Join(dir, "config.json")); err != nil {
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}

// DatabasePath 返回数据目录下的 SQLite 文件路径。
func DatabasePath(dir string) string {
	return filepath.Join(dir, "chat.db")
}
