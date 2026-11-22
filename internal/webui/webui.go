// Package webui 嵌入构建后的 React 客户端；未构建时退回服务端占位页。
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var assets embed.FS

// Available 表示当前二进制是否包含可用的 Web UI。
func Available() bool {
	_, err := fs.Stat(assets, "dist/index.html")
	return err == nil
}

// Handler 返回静态文件处理器；资源文件名带内容哈希，可以长期缓存。
func Handler() http.Handler {
	dist, err := fs.Sub(assets, "dist")
	if err != nil {
		return http.NotFoundHandler()
	}
	files := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/static/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}
