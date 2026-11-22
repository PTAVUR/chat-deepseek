package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"gitlab.com/elegantgx/chat-deepseek/internal/chat"
	"gitlab.com/elegantgx/chat-deepseek/internal/filestore"
)

type chatListResponse struct {
	Chats []chat.Summary `json:"chats"`
}

type chatResponse struct {
	Session json.RawMessage `json:"session"`
	Version int64           `json:"version"`
}

type createChatRequest struct {
	Session json.RawMessage `json:"session"`
}

type updateChatRequest struct {
	Session json.RawMessage `json:"session"`
	Version int64           `json:"version"`
}

func (s *Server) handleListChats(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	summaries, err := s.chats.List(r.Context(), user.ID)
	if err != nil {
		s.writeChatError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, chatListResponse{Chats: summaries})
}

func (s *Server) handleCreateChat(w http.ResponseWriter, r *http.Request) {
	var request createChatRequest
	if err := decodeJSONLimit(w, r, &request, chat.MaxSessionSize+(1<<20)); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "请求格式不正确")
		return
	}
	if len(request.Session) == 0 {
		writeAPIError(w, http.StatusBadRequest, "invalid_session", "缺少会话数据")
		return
	}
	user := userFromContext(r.Context())
	stored, err := s.chats.Create(r.Context(), user.ID, request.Session)
	if err != nil {
		s.writeChatError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, chatResponse{Session: stored.Session, Version: stored.Version})
}

func (s *Server) handleGetChat(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	stored, err := s.chats.Get(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		s.writeChatError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, chatResponse{Session: stored.Session, Version: stored.Version})
}

func (s *Server) handleUpdateChat(w http.ResponseWriter, r *http.Request) {
	var request updateChatRequest
	if err := decodeJSONLimit(w, r, &request, chat.MaxSessionSize+(1<<20)); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "请求格式不正确")
		return
	}
	if len(request.Session) == 0 || request.Version < 1 {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "缺少会话数据或版本号")
		return
	}
	user := userFromContext(r.Context())
	stored, err := s.chats.Update(r.Context(), user.ID, r.PathValue("id"), request.Session, request.Version)
	if err != nil {
		s.writeChatError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, chatResponse{Session: stored.Session, Version: stored.Version})
}

func (s *Server) handleDeleteChat(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	fileIDs, err := s.chats.Delete(r.Context(), user.ID, r.PathValue("id"))
	if err != nil {
		s.writeChatError(w, err)
		return
	}
	// 会话已经删除；附件清理失败只留日志，避免客户端收到失败后重试却只得到 404。
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Second)
	defer cancel()
	s.cleanupAttachments(ctx, user.ID, fileIDs)
	w.WriteHeader(http.StatusNoContent)
}

// cleanupAttachments 删除不再被任何会话引用的附件；被其他会话共用的原件保留。
func (s *Server) cleanupAttachments(ctx context.Context, userID string, fileIDs []string) {
	if len(fileIDs) == 0 || s.files == nil {
		return
	}
	referenced, err := s.chats.ReferencedFileIDs(ctx, userID)
	if err != nil {
		s.logger.Error("list referenced attachments failed", "error", err)
		return
	}
	for _, fileID := range fileIDs {
		if _, ok := referenced[fileID]; ok {
			continue
		}
		if err := s.files.Delete(ctx, userID, fileID); err != nil &&
			!errors.Is(err, filestore.ErrNotFound) {
			s.logger.Error("delete attachment failed", "file_id", fileID, "error", err)
		}
	}
}

func (s *Server) writeChatError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, chat.ErrNotFound):
		writeAPIError(w, http.StatusNotFound, "chat_not_found", "会话不存在")
	case errors.Is(err, chat.ErrConflict):
		writeAPIError(w, http.StatusConflict, "version_conflict", "会话已在其他窗口更新，请刷新后重试")
	case errors.Is(err, chat.ErrInvalidSession):
		writeAPIError(w, http.StatusBadRequest, "invalid_session", "会话数据不合法")
	default:
		s.logger.Error("chat store failed", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "会话操作失败")
	}
}
