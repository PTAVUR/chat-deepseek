package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

const maxImportSize = 256 << 20

type importRequest struct {
	Sessions []json.RawMessage `json:"sessions"`
}

// handleImport 用一批已校验的会话原子替换当前用户的全部会话。
// 附件必须已经上传；替换成功后清理旧会话不再引用的附件。
func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	var request importRequest
	if err := decodeJSONLimit(w, r, &request, maxImportSize); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "请求格式不正确")
		return
	}
	if len(request.Sessions) == 0 {
		writeAPIError(w, http.StatusBadRequest, "invalid_session", "缺少会话数据")
		return
	}
	user := userFromContext(r.Context())
	var oldFileIDs []string
	if s.files != nil {
		ids, err := s.files.IDs(r.Context(), user.ID)
		if err != nil {
			s.logger.Error("list attachments before import failed", "error", err)
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "导入失败")
			return
		}
		oldFileIDs = ids
	}
	sessions := make([][]byte, 0, len(request.Sessions))
	for _, session := range request.Sessions {
		sessions = append(sessions, session)
	}
	if err := s.chats.Import(r.Context(), user.ID, sessions); err != nil {
		s.writeChatError(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Second)
	defer cancel()
	s.cleanupAttachments(ctx, user.ID, oldFileIDs)
	w.WriteHeader(http.StatusNoContent)
}
