package backup

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var archiveTimePattern = regexp.MustCompile(`\d{8}-\d{6}`)

// Archive 是备份目录里的一个 ZIP。
type Archive struct {
	Name string
	Path string
	Size int64
	Time time.Time
}

// List 返回备份目录里的 ZIP，按时间从新到旧排列。
// 目录不存在时返回空列表，便于首次使用直接执行 list。
func List(backupDir string) ([]Archive, error) {
	dir, err := filepath.Abs(backupDir)
	if err != nil {
		return nil, fmt.Errorf("resolve backup directory: %w", err)
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取备份地址: %w", err)
	}
	archives := make([]Archive, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".zip") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("读取备份 %s: %w", entry.Name(), err)
		}
		if !info.Mode().IsRegular() {
			continue
		}
		archives = append(archives, Archive{
			Name: entry.Name(),
			Path: filepath.Join(dir, entry.Name()),
			Size: info.Size(),
			Time: archiveTime(entry.Name(), info.ModTime()),
		})
	}
	sort.Slice(archives, func(i, j int) bool {
		if !archives[i].Time.Equal(archives[j].Time) {
			return archives[i].Time.After(archives[j].Time)
		}
		return archives[i].Name > archives[j].Name
	})
	return archives, nil
}

// archiveTime 优先使用文件名里的时间；文件名不含时间时退回修改时间。
func archiveTime(name string, fallback time.Time) time.Time {
	match := archiveTimePattern.FindString(name)
	if match == "" {
		return fallback
	}
	stamp, err := time.ParseInLocation("20060102-150405", match, time.Local)
	if err != nil {
		return fallback
	}
	return stamp
}
