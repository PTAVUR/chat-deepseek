package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/elegantgx/chat-deepseek/internal/chat"
	"github.com/elegantgx/chat-deepseek/internal/deepseek"
	"github.com/elegantgx/chat-deepseek/internal/filestore"
	"github.com/elegantgx/chat-deepseek/internal/settings"
)

func (s *Server) handleStream(w http.ResponseWriter, r *http.Request) {
	if s.deepseek == nil {
		writeAPIError(w, http.StatusServiceUnavailable, "upstream_unavailable", "DeepSeek 代理未启用")
		return
	}
	apiKey, ok := s.requireAPIKey(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, chat.MaxSessionSize)
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			writeAPIError(w, http.StatusRequestEntityTooLarge, "request_too_large", "请求内容过大")
			return
		}
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "读取请求失败")
		return
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "请求必须是 JSON 对象")
		return
	}
	if body == nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "请求必须是 JSON 对象")
		return
	}
	user := userFromContext(r.Context())
	hasFiles, err := s.rewriteFileIDs(r.Context(), user.ID, apiKey, body)
	if err != nil {
		s.writeDeepSeekError(w, err)
		return
	}
	body["stream"] = true
	payload, err := json.Marshal(body)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "请求内容不合法")
		return
	}
	response, err := s.deepseek.Messages(r.Context(), apiKey, payload, hasFiles)
	if err != nil {
		s.writeDeepSeekError(w, err)
		return
	}
	defer func() { _ = response.Body.Close() }()

	copyUpstreamHeaders(w, response)
	w.WriteHeader(response.StatusCode)
	flusher, _ := w.(http.Flusher)
	buffer := make([]byte, 32*1024)
	for {
		count, err := response.Body.Read(buffer)
		if count > 0 {
			if _, writeErr := w.Write(buffer[:count]); writeErr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			return
		}
	}
}

func (s *Server) requireAPIKey(w http.ResponseWriter, r *http.Request) (string, bool) {
	user := userFromContext(r.Context())
	apiKey, err := s.userSettings.APIKey(r.Context(), user.ID)
	if errors.Is(err, settings.ErrNoAPIKey) {
		writeAPIError(w, http.StatusBadRequest, "api_key_missing", "请先在设置中保存 API Key")
		return "", false
	}
	if err != nil {
		s.logger.Error("read api key failed", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "读取 API Key 失败")
		return "", false
	}
	return apiKey, true
}

// rewriteFileIDs 把本地附件 ID 换成 DeepSeek file_id，并在需要时惰性上传附件。
func (s *Server) rewriteFileIDs(ctx context.Context, userID, apiKey string, value any) (bool, error) {
	switch node := value.(type) {
	case map[string]any:
		hasFiles := false
		for key, item := range node {
			if key == "file_id" {
				hasFiles = true
				fileID, ok := item.(string)
				if !ok || fileID == "" {
					continue
				}
				remote, found, err := s.resolveFileID(ctx, userID, apiKey, fileID)
				if err != nil {
					return true, err
				}
				if found {
					node[key] = remote
				}
				continue
			}
			nested, err := s.rewriteFileIDs(ctx, userID, apiKey, item)
			if err != nil {
				return hasFiles || nested, err
			}
			hasFiles = hasFiles || nested
		}
		return hasFiles, nil
	case []any:
		hasFiles := false
		for _, item := range node {
			nested, err := s.rewriteFileIDs(ctx, userID, apiKey, item)
			if err != nil {
				return hasFiles || nested, err
			}
			hasFiles = hasFiles || nested
		}
		return hasFiles, nil
	default:
		return false, nil
	}
}

func (s *Server) resolveFileID(
	ctx context.Context,
	userID, apiKey, fileID string,
) (remote string, found bool, err error) {
	if s.files == nil {
		return "", false, nil
	}
	file, err := s.files.Get(ctx, userID, fileID)
	if errors.Is(err, filestore.ErrNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if file.ServerID != "" {
		return file.ServerID, true, nil
	}
	metadata, handle, err := s.files.Open(ctx, userID, fileID)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = handle.Close() }()
	purpose := metadata.Purpose
	if purpose == "" && !metadata.IsImage {
		purpose = inferPurpose(metadata.Name, false)
	}
	remote, err = s.deepseek.UploadFile(ctx, apiKey, metadata.Name, purpose, handle)
	if err != nil {
		return "", false, err
	}
	if err := s.files.SetServerID(ctx, userID, fileID, remote); err != nil {
		// 远端已经上传成功；缓存失败不应让本轮聊天失败，下次会再上传一次。
		s.logger.Warn("cache remote file id failed", "error", err)
	}
	return remote, true, nil
}

func copyUpstreamHeaders(w http.ResponseWriter, response *http.Response) {
	for _, name := range []string{"Content-Type", "Cache-Control", "X-Request-Id"} {
		if value := response.Header.Get(name); value != "" {
			w.Header().Set(name, value)
		}
	}
	w.Header().Set("X-Accel-Buffering", "no")
	w.Header().Set("Cache-Control", "no-store")
}

func (s *Server) writeDeepSeekError(w http.ResponseWriter, err error) {
	var apiErr *deepseek.APIError
	if errors.As(err, &apiErr) {
		contentType := "application/json; charset=utf-8"
		if !json.Valid(apiErr.Body) {
			contentType = "text/plain; charset=utf-8"
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(apiErr.StatusCode)
		_, _ = w.Write(apiErr.Body)
		return
	}
	s.logger.Error("deepseek request failed", "error", err)
	writeAPIError(w, http.StatusBadGateway, "upstream_error", "连接 DeepSeek 失败")
}

func (s *Server) handleASR(w http.ResponseWriter, r *http.Request) {
	apiKey, ok := s.requireAPIKey(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, filestore.MaxFileSize+(1<<20))
	response, err := s.deepseek.ASR(r.Context(), apiKey, r.Header.Get("Content-Type"), r.Body)
	if err != nil {
		s.writeDeepSeekError(w, err)
		return
	}
	defer func() { _ = response.Body.Close() }()
	if contentType := response.Header.Get("Content-Type"); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, response.Body)
}

func (s *Server) handleTTS(w http.ResponseWriter, r *http.Request) {
	apiKey, ok := s.requireAPIKey(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "读取请求失败")
		return
	}
	if !json.Valid(body) {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "请求必须是 JSON")
		return
	}
	response, err := s.deepseek.TTS(r.Context(), apiKey, body)
	if err != nil {
		s.writeDeepSeekError(w, err)
		return
	}
	defer func() { _ = response.Body.Close() }()
	if contentType := response.Header.Get("Content-Type"); contentType != "" {
		w.Header().Set("Content-Type", contentType)
	} else {
		w.Header().Set("Content-Type", "audio/pcm")
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, response.Body)
}
