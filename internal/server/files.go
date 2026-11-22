package server

import (
	"errors"
	"io"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"

	"gitlab.com/elegantgx/chat-deepseek/internal/filestore"
)

func (s *Server) handleUploadFile(w http.ResponseWriter, r *http.Request) {
	if s.files == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "files_unavailable", "文件存储未启用")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, filestore.MaxFileSize+(1<<20))
	reader, err := r.MultipartReader()
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "请使用 multipart/form-data 上传")
		return
	}
	user := userFromContext(r.Context())
	var purpose string
	var isImage bool
	var uploaded *filestore.File
	for uploaded == nil {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				writeAPIError(w, http.StatusRequestEntityTooLarge, "file_too_large", "文件超过大小限制")
				return
			}
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "读取上传内容失败")
			return
		}
		switch part.FormName() {
		case "purpose":
			value, _ := io.ReadAll(io.LimitReader(part, 128))
			purpose = strings.TrimSpace(string(value))
		case "isImage":
			value, _ := io.ReadAll(io.LimitReader(part, 16))
			parsed, _ := strconv.ParseBool(strings.TrimSpace(string(value)))
			isImage = parsed
		case "file":
			name := part.FileName()
			contentType := normalizeMediaType(part.Header.Get("Content-Type"))
			if strings.HasPrefix(strings.ToLower(contentType), "image/") {
				isImage = true
			}
			if purpose == "" {
				purpose = inferPurpose(name, isImage)
			}
			uploaded, err = s.files.Create(
				r.Context(),
				user.ID,
				name,
				contentType,
				purpose,
				isImage,
				0,
				part,
			)
			_ = part.Close()
			if err != nil {
				s.writeFileError(w, err)
				return
			}
		default:
			_, _ = io.Copy(io.Discard, io.LimitReader(part, 1<<20))
		}
		_ = part.Close()
	}
	if uploaded == nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "缺少文件")
		return
	}
	writeJSON(w, http.StatusCreated, uploaded)
}

func (s *Server) handleDownloadFile(w http.ResponseWriter, r *http.Request) {
	if s.files == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "files_unavailable", "文件存储未启用")
		return
	}
	user := userFromContext(r.Context())
	file, handle, err := s.files.Open(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		s.writeFileError(w, err)
		return
	}
	defer func() { _ = handle.Close() }()

	contentType := normalizeMediaType(file.MIMEType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": file.Name})
	if disposition == "" {
		disposition = "attachment"
	}
	w.Header().Set("Content-Disposition", disposition)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeContent(w, r, file.Name, time.UnixMilli(file.CreatedAt), handle)
}

func (s *Server) handleDeleteFile(w http.ResponseWriter, r *http.Request) {
	if s.files == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "files_unavailable", "文件存储未启用")
		return
	}
	user := userFromContext(r.Context())
	if err := s.files.Delete(r.Context(), user.ID, r.PathValue("id")); err != nil {
		s.writeFileError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) writeFileError(w http.ResponseWriter, err error) {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		writeAPIError(w, http.StatusRequestEntityTooLarge, "file_too_large", "文件超过大小限制")
		return
	}
	switch {
	case errors.Is(err, filestore.ErrNotFound):
		writeAPIError(w, http.StatusNotFound, "file_not_found", "文件不存在")
	case errors.Is(err, filestore.ErrTooLarge):
		writeAPIError(w, http.StatusRequestEntityTooLarge, "file_too_large", "文件超过大小限制")
	case errors.Is(err, filestore.ErrInvalidFile):
		writeAPIError(w, http.StatusBadRequest, "invalid_file", "文件信息不合法")
	default:
		s.logger.Error("file operation failed", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "文件操作失败")
	}
}

func inferPurpose(name string, isImage bool) string {
	if isImage {
		return ""
	}
	switch strings.ToLower(path.Ext(name)) {
	case ".pdf", ".doc", ".docx", ".ppt", ".pptx":
		return "rich_layout_recognition"
	default:
		return "user_data"
	}
}

func normalizeMediaType(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		return ""
	}
	return mediaType
}
