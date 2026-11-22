package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"gitlab.com/elegantgx/chat-deepseek/internal/chat"
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
	deleted := false
	if value := r.URL.Query().Get("deleted"); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "deleted 参数不正确")
			return
		}
		deleted = parsed
	}
	user := userFromContext(r.Context())
	summaries, err := s.chats.List(r.Context(), user.ID, deleted)
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
	if err := s.chats.SoftDelete(r.Context(), user.ID, r.PathValue("id")); err != nil {
		s.writeChatError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRestoreChat(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if err := s.chats.Restore(r.Context(), user.ID, r.PathValue("id")); err != nil {
		s.writeChatError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePurgeChat(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if err := s.chats.Purge(r.Context(), user.ID, r.PathValue("id")); err != nil {
		s.writeChatError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
