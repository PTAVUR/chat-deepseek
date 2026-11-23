// Package backup 把数据目录打包成可手工恢复的 ZIP。
package backup

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ErrDatabaseInUse 表示数据目录里仍有 SQLite 的 WAL/SHM 文件，服务可能正在运行。
var ErrDatabaseInUse = errors.New("backup: database is in use")

// DefaultDir 返回默认备份地址：数据目录下的 backups/。
func DefaultDir(dataDir string) string {
	return filepath.Join(dataDir, "backups")
}

// Create 把 dataDir 打包成 ZIP；backupDir 为空时使用 DefaultDir，
// output 为空时写到 backupDir/chat-deepseek-<时间>.zip。
// 备份前要求 chat.db-wal 和 chat.db-shm 都不存在，避免拷贝到运行中的数据库。
func Create(dataDir, backupDir, output string) (string, error) {
	dir, err := filepath.Abs(dataDir)
	if err != nil {
		return "", fmt.Errorf("resolve data directory: %w", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "chat.db")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("数据目录中没有 chat.db：%s", dir)
		}
		return "", fmt.Errorf("检查 chat.db: %w", err)
	}
	if err := ensureStopped(dir); err != nil {
		return "", err
	}
	if backupDir == "" {
		backupDir = DefaultDir(dir)
	}
	backupDir, err = filepath.Abs(backupDir)
	if err != nil {
		return "", fmt.Errorf("resolve backup directory: %w", err)
	}
	if backupDir == dir {
		return "", errors.New("备份地址不能是数据目录本身")
	}
	if output == "" {
		output = filepath.Join(backupDir, ArchiveName(time.Now()))
	}
	output, err = filepath.Abs(output)
	if err != nil {
		return "", fmt.Errorf("resolve output path: %w", err)
	}
	for _, name := range []string{"chat.db", "master.key", "config.json"} {
		if output == filepath.Join(dir, name) {
			return "", fmt.Errorf("备份输出不能覆盖数据文件：%s", name)
		}
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o700); err != nil {
		return "", fmt.Errorf("create backup directory: %w", err)
	}

	temp, err := os.CreateTemp(filepath.Dir(output), ".backup-*.tmp")
	if err != nil {
		return "", fmt.Errorf("create backup file: %w", err)
	}
	tempPath := temp.Name()
	defer func() {
		_ = temp.Close()
		_ = os.Remove(tempPath)
	}()
	skipRel := relativeInside(dir, backupDir)
	if err := writeArchive(temp, dir, skipRel, output, tempPath); err != nil {
		return "", err
	}
	if err := ensureStopped(dir); err != nil {
		return "", err
	}
	if err := temp.Close(); err != nil {
		return "", fmt.Errorf("close backup file: %w", err)
	}
	if err := os.Rename(tempPath, output); err != nil {
		return "", fmt.Errorf("write backup file: %w", err)
	}
	return output, nil
}

func ensureStopped(dir string) error {
	for _, name := range []string{"chat.db-wal", "chat.db-shm"} {
		_, err := os.Stat(filepath.Join(dir, name))
		if err == nil {
			return fmt.Errorf("%w：检测到 %s，请先停止服务", ErrDatabaseInUse, name)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("检查 %s: %w", name, err)
		}
	}
	return nil
}

func writeArchive(dst io.Writer, dataDir, skipRel, output, tempPath string) error {
	writer := zip.NewWriter(dst)
	if err := addTree(writer, dataDir, skipRel, output, tempPath); err != nil {
		_ = writer.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("finish backup archive: %w", err)
	}
	return nil
}

func addTree(writer *zip.Writer, dataDir, skipRel, output, tempPath string) error {
	return filepath.WalkDir(dataDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(dataDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		// 备份目录不应进入归档，否则会把旧备份和本次输出一起打包。
		// 默认的 backups/ 始终跳过；自定义备份地址在数据目录内时也跳过。
		slashRel := filepath.ToSlash(rel)
		if slashRel == "backups" || (skipRel != "" && slashRel == skipRel) {
			return filepath.SkipDir
		}
		if path == output || path == tempPath {
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Name() == "chat.db-wal" || entry.Name() == "chat.db-shm" {
			return fmt.Errorf("%w：检测到 %s，请先停止服务", ErrDatabaseInUse, entry.Name())
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(rel)
		header.Method = zip.Deflate
		part, err := writer.CreateHeader(header)
		if err != nil {
			return err
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(part, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		return nil
	})
}

// ArchiveName 返回默认备份文件名。
func ArchiveName(now time.Time) string {
	return "chat-deepseek-" + now.Format("20060102-150405") + ".zip"
}

// relativeInside 返回 target 相对 base 的路径；target 不在 base 内时返回空串。
func relativeInside(base, target string) string {
	rel, err := filepath.Rel(base, target)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	return filepath.ToSlash(rel)
}
