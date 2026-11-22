// Package config 负责便携式数据目录和服务器配置。
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Config 是非敏感的服务端配置；API Key 等秘密数据存在数据库或单独文件里。
type Config struct {
	Listen    string `json:"listen"`
	PublicURL string `json:"public_url,omitempty"`
}

// Default 返回默认配置：只监听本机，使用非特权端口。
func Default() Config {
	return Config{Listen: "127.0.0.1:8090"}
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
	return cfg, nil
}

// Save 原子写入 config.json。
func Save(dir string, cfg Config) error {
	if err := EnsureDataDir(dir); err != nil {
		return err
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	data = append(data, '\n')
	tmp := filepath.Join(dir, "config.json.tmp")
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write config: %w", err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, "config.json")); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace config: %w", err)
	}
	return nil
}

// DatabasePath 返回数据目录下的 SQLite 文件路径。
func DatabasePath(dir string) string {
	return filepath.Join(dir, "chat.db")
}
