package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/elegantgx/chat-deepseek/internal/settings"
)

type settingsResponse struct {
	Preferences json.RawMessage `json:"preferences"`
	HasAPIKey   bool            `json:"hasApiKey"`
	UpdatedAt   int64           `json:"updatedAt"`
}

func toSettingsResponse(value settings.Settings) settingsResponse {
	return settingsResponse{
		Preferences: value.Preferences,
		HasAPIKey:   value.HasAPIKey,
		UpdatedAt:   value.UpdatedAt,
	}
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	value, err := s.userSettings.Get(r.Context(), user.ID)
	if err != nil {
		s.logger.Error("read settings failed", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "读取设置失败")
		return
	}
	writeJSON(w, http.StatusOK, toSettingsResponse(value))
}

type updateSettingsRequest struct {
	Preferences json.RawMessage `json:"preferences"`
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var request updateSettingsRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "请求格式不正确")
		return
	}
	user := userFromContext(r.Context())
	value, err := s.userSettings.SavePreferences(r.Context(), user.ID, request.Preferences)
	if err != nil {
		if errors.Is(err, settings.ErrInvalidPreferences) {
			writeAPIError(w, http.StatusBadRequest, "invalid_preferences", "设置内容不合法")
			return
		}
		s.logger.Error("save settings failed", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "保存设置失败")
		return
	}
	writeJSON(w, http.StatusOK, toSettingsResponse(value))
}

type setAPIKeyRequest struct {
	APIKey string `json:"apiKey"`
}

func (s *Server) handleSetAPIKey(w http.ResponseWriter, r *http.Request) {
	var request setAPIKeyRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "请求格式不正确")
		return
	}
	user := userFromContext(r.Context())
	hasKey, err := s.userSettings.SetAPIKey(r.Context(), user.ID, request.APIKey)
	if err != nil {
		if errors.Is(err, settings.ErrInvalidAPIKey) {
			writeAPIError(w, http.StatusBadRequest, "invalid_api_key", "API Key 格式不正确")
			return
		}
		s.logger.Error("save api key failed", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "保存 API Key 失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"hasApiKey": hasKey})
}

func (s *Server) handleClearAPIKey(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	if _, err := s.userSettings.SetAPIKey(r.Context(), user.ID, ""); err != nil {
		s.logger.Error("clear api key failed", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "清除 API Key 失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"hasApiKey": false})
}

func (s *Server) handleVerifyAPIKey(w http.ResponseWriter, r *http.Request) {
	var request setAPIKeyRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "请求格式不正确")
		return
	}
	apiKey := strings.TrimSpace(request.APIKey)
	if apiKey == "" {
		user := userFromContext(r.Context())
		var err error
		apiKey, err = s.userSettings.APIKey(r.Context(), user.ID)
		if errors.Is(err, settings.ErrNoAPIKey) {
			writeAPIError(w, http.StatusBadRequest, "api_key_missing", "请先填写 API Key")
			return
		}
		if err != nil {
			s.logger.Error("read api key for verify failed", "error", err)
			writeAPIError(w, http.StatusInternalServerError, "internal_error", "读取 API Key 失败")
			return
		}
	}
	if len(apiKey) > settings.MaxAPIKeyLength {
		writeAPIError(w, http.StatusBadRequest, "invalid_api_key", "API Key 格式不正确")
		return
	}
	if err := s.deepseek.Models(r.Context(), apiKey); err != nil {
		s.writeDeepSeekError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
