package backup

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

var restoreItems = []string{"config.json", "chat.db", "master.key", "files"}

// Restore 用备份覆盖数据目录里的 config.json、chat.db、master.key 和 files/。
// 备份里缺少某一项时，会删除当前对应的文件/目录，保证恢复到备份时的状态。
func Restore(dataDir, backupDir, name string) (string, error) {
	dir, err := filepath.Abs(dataDir)
	if err != nil {
		return "", fmt.Errorf("resolve data directory: %w", err)
	}
	if strings.TrimSpace(backupDir) == "" {
		backupDir = DefaultDir(dir)
	}
	archivePath, err := resolveArchive(backupDir, name)
	if err != nil {
		return "", err
	}
	if err := ensureStopped(dir); err != nil {
		return "", err
	}

	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", fmt.Errorf("打开备份 %s: %w", filepath.Base(archivePath), err)
	}
	hasDatabase := false
	for _, file := range reader.File {
		if file.FileInfo().IsDir() {
			continue
		}
		cleaned, ok := cleanArchivePath(file.Name)
		if !ok {
			_ = reader.Close()
			return "", fmt.Errorf("备份包含非法路径：%s", file.Name)
		}
		if !isDataEntry(cleaned) {
			continue
		}
		if !file.FileInfo().Mode().IsRegular() {
			_ = reader.Close()
			return "", fmt.Errorf("备份包含不支持的文件类型：%s", file.Name)
		}
		if cleaned == "chat.db" {
			hasDatabase = true
		}
	}
	if !hasDatabase {
		_ = reader.Close()
		return "", errors.New("备份中没有 chat.db")
	}

	parent := filepath.Dir(dir)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		_ = reader.Close()
		return "", fmt.Errorf("create data directory: %w", err)
	}
	stage, err := os.MkdirTemp(parent, ".restore-*")
	if err != nil {
		_ = reader.Close()
		return "", fmt.Errorf("创建恢复临时目录: %w", err)
	}
	keepStage := false
	defer func() {
		if !keepStage {
			_ = os.RemoveAll(stage)
		}
	}()

	if err := extractArchive(reader.File, stage); err != nil {
		_ = reader.Close()
		return "", err
	}
	if err := reader.Close(); err != nil {
		return "", fmt.Errorf("关闭备份 %s: %w", filepath.Base(archivePath), err)
	}

	if err := ensureStopped(dir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create data directory: %w", err)
	}
	if err := swapRestoreItems(dir, stage); err != nil {
		// 回滚可能只完成一部分；保留临时目录，避免把仅剩的旧数据一起删掉。
		keepStage = true
		return "", fmt.Errorf("%w；临时目录保留在 %s", err, stage)
	}
	return archivePath, nil
}

func resolveArchive(backupDir, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("请指定要恢复的备份")
	}
	candidates := make([]string, 0, 2)
	if filepath.IsAbs(name) {
		candidates = append(candidates, name)
	} else {
		candidates = append(candidates, filepath.Join(backupDir, name))
		if strings.ContainsAny(name, `/\`) {
			candidates = append(candidates, name)
		}
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil {
			if !info.Mode().IsRegular() {
				return "", fmt.Errorf("备份不是文件：%s", candidate)
			}
			absolute, err := filepath.Abs(candidate)
			if err != nil {
				return "", fmt.Errorf("resolve backup: %w", err)
			}
			return absolute, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("检查备份 %s: %w", candidate, err)
		}
	}
	return "", fmt.Errorf("备份不存在：%s", name)
}

func cleanArchivePath(name string) (string, bool) {
	if name == "" || strings.ContainsRune(name, '\\') || path.IsAbs(name) {
		return "", false
	}
	cleaned := path.Clean(name)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", false
	}
	return cleaned, true
}

func isDataEntry(name string) bool {
	switch name {
	case "config.json", "chat.db", "master.key":
		return true
	}
	return strings.HasPrefix(name, "files/") && len(name) > len("files/")
}

func extractArchive(files []*zip.File, stage string) error {
	for _, file := range files {
		if file.FileInfo().IsDir() {
			continue
		}
		cleaned, ok := cleanArchivePath(file.Name)
		if !ok {
			return fmt.Errorf("备份包含非法路径：%s", file.Name)
		}
		if !isDataEntry(cleaned) {
			continue
		}
		if !file.FileInfo().Mode().IsRegular() {
			return fmt.Errorf("备份包含不支持的文件类型：%s", file.Name)
		}
		target := filepath.Join(stage, filepath.FromSlash(cleaned))
		if relativeInside(stage, target) == "" {
			return fmt.Errorf("备份包含非法路径：%s", file.Name)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return fmt.Errorf("create restore directory: %w", err)
		}
		handle, err := file.Open()
		if err != nil {
			return fmt.Errorf("读取备份中的 %s: %w", cleaned, err)
		}
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			_ = handle.Close()
			return fmt.Errorf("写入恢复文件 %s: %w", cleaned, err)
		}
		_, copyErr := io.Copy(out, handle)
		closeCopyErr := out.Close()
		closeSourceErr := handle.Close()
		if copyErr != nil {
			return fmt.Errorf("写入恢复文件 %s: %w", cleaned, copyErr)
		}
		if closeCopyErr != nil {
			return fmt.Errorf("写入恢复文件 %s: %w", cleaned, closeCopyErr)
		}
		if closeSourceErr != nil {
			return fmt.Errorf("读取备份中的 %s: %w", cleaned, closeSourceErr)
		}
	}
	return nil
}

// swapRestoreItems 先挪走旧数据，再放入新数据；中途失败会尝试回滚。
func swapRestoreItems(dir, stage string) (err error) {
	previous := filepath.Join(stage, "previous")
	if err := os.Mkdir(previous, 0o700); err != nil {
		return fmt.Errorf("create restore rollback directory: %w", err)
	}
	movedOld := make([]string, 0, len(restoreItems))
	placedNew := make([]string, 0, len(restoreItems))
	defer func() {
		if err == nil {
			return
		}
		for index := len(placedNew) - 1; index >= 0; index-- {
			_ = os.RemoveAll(filepath.Join(dir, placedNew[index]))
		}
		for index := len(movedOld) - 1; index >= 0; index-- {
			_ = os.Rename(filepath.Join(previous, movedOld[index]), filepath.Join(dir, movedOld[index]))
		}
	}()

	for _, item := range restoreItems {
		target := filepath.Join(dir, item)
		staged := filepath.Join(stage, item)

		if _, statErr := os.Lstat(target); statErr == nil {
			if err = os.Rename(target, filepath.Join(previous, item)); err != nil {
				return fmt.Errorf("备份现有 %s: %w", item, err)
			}
			movedOld = append(movedOld, item)
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return fmt.Errorf("检查现有 %s: %w", item, statErr)
		}

		if _, statErr := os.Lstat(staged); statErr == nil {
			if err = os.Rename(staged, target); err != nil {
				return fmt.Errorf("恢复 %s: %w", item, err)
			}
			placedNew = append(placedNew, item)
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return fmt.Errorf("检查备份中的 %s: %w", item, statErr)
		}
	}
	return nil
}
