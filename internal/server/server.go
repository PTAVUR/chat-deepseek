// Package server 提供 HTTP API、登录会话和静态占位页。
package server

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"gitlab.com/elegantgx/chat-deepseek/internal/account"
	"gitlab.com/elegantgx/chat-deepseek/internal/auth"
	"gitlab.com/elegantgx/chat-deepseek/internal/buildinfo"
	"gitlab.com/elegantgx/chat-deepseek/internal/config"
)

// Server 持有 API 依赖；Handler 返回可挂到 http.Server 的处理链。
type Server struct {
	cfg      config.Config
	db       *sql.DB
	accounts *account.Store
	sessions *auth.SessionStore
	logger   *slog.Logger
	limiter  *loginLimiter
	now      func() time.Time
}

// New 创建服务端。
func New(cfg config.Config, db *sql.DB, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		cfg:      cfg,
		db:       db,
		accounts: account.NewStore(db),
		sessions: auth.NewSessionStore(db),
		logger:   logger,
		limiter:  newLoginLimiter(10, 15*time.Minute),
		now:      time.Now,
	}
}

// Handler 组装路由与中间件。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /{$}", s.handleRoot)
	mux.HandleFunc("POST /api/v1/auth/login", s.handleLogin)
	mux.Handle("GET /api/v1/auth/me", s.withAuth(http.HandlerFunc(s.handleMe)))
	mux.Handle("POST /api/v1/auth/logout", s.withAuth(s.withCSRF(http.HandlerFunc(s.handleLogout))))
	mux.Handle(
		"POST /api/v1/auth/password",
		s.withAuth(s.withCSRF(http.HandlerFunc(s.handlePassword))),
	)
	return s.securityHeaders(s.recoverPanic(mux))
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "ok",
		"name":    "chat-deepseek",
		"version": buildinfo.Version,
	})
}

func (s *Server) handleRoot(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = fmt.Fprintf(w, `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><title>chat-deepseek</title></head>
<body><h1>chat-deepseek</h1><p>服务端已启动，版本 %s。</p>
<p>Web UI 尚未接入；当前可用 API：<code>/api/v1/auth/login</code>、<code>/healthz</code>。</p></body></html>`,
		buildinfo.Version,
	)
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type userJSON struct {
	ID        string   `json:"id"`
	Username  string   `json:"username"`
	Groups    []string `json:"groups"`
	Disabled  bool     `json:"disabled"`
	CreatedAt int64    `json:"createdAt"`
	UpdatedAt int64    `json:"updatedAt"`
}

type sessionResponse struct {
	User      userJSON `json:"user"`
	CSRF      string   `json:"csrf"`
	ExpiresAt int64    `json:"expiresAt"`
}

type meResponse struct {
	sessionResponse
	HasAPIKey bool `json:"hasApiKey"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeAPIError(w, http.StatusForbidden, "forbidden_origin", "请求来源不允许")
		return
	}
	var request loginRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "请求格式不正确")
		return
	}
	username := strings.TrimSpace(request.Username)
	if username == "" || request.Password == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "用户名和密码不能为空")
		return
	}
	limitKey := clientIP(r) + "|" + strings.ToLower(username)
	if allowed, retry := s.limiter.Allow(limitKey, s.now()); !allowed {
		w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())+1))
		writeAPIError(w, http.StatusTooManyRequests, "rate_limited", "尝试次数过多，请稍后再试")
		return
	}

	user, err := s.accounts.GetByUsername(r.Context(), username)
	if err != nil {
		if errors.Is(err, account.ErrUserNotFound) {
			auth.VerifyDummy(request.Password)
			s.limiter.Fail(limitKey, s.now())
			writeAPIError(w, http.StatusUnauthorized, "invalid_credentials", "用户名或密码错误")
			return
		}
		s.logger.Error("login lookup failed", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "登录失败")
		return
	}
	valid, err := s.accounts.VerifyPassword(r.Context(), user.ID, request.Password)
	if err != nil {
		s.logger.Error("login verify failed", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "登录失败")
		return
	}
	if !valid {
		s.limiter.Fail(limitKey, s.now())
		writeAPIError(w, http.StatusUnauthorized, "invalid_credentials", "用户名或密码错误")
		return
	}
	if user.Disabled {
		writeAPIError(w, http.StatusForbidden, "account_disabled", "账户已被禁用")
		return
	}

	token, tokenHash, csrf, err := auth.NewSessionSecrets()
	if err != nil {
		s.logger.Error("session secret failed", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "登录失败")
		return
	}
	session, err := s.sessions.Create(
		r.Context(),
		user.ID,
		tokenHash,
		csrf,
		r.UserAgent(),
		clientIP(r),
		s.now(),
	)
	if err != nil {
		s.logger.Error("session create failed", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "登录失败")
		return
	}
	s.limiter.Reset(limitKey)
	s.setSessionCookie(w, r, token, session.ExpiresAt)
	writeJSON(w, http.StatusOK, sessionResponse{
		User:      toUserJSON(user),
		CSRF:      session.CSRF,
		ExpiresAt: session.ExpiresAt,
	})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user := userFromContext(r.Context())
	session := sessionFromContext(r.Context())
	hasKey, err := s.hasAPIKey(r.Context(), user.ID)
	if err != nil {
		s.logger.Error("read api key status failed", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "读取设置失败")
		return
	}
	writeJSON(w, http.StatusOK, meResponse{
		sessionResponse: sessionResponse{
			User:      toUserJSON(user),
			CSRF:      session.CSRF,
			ExpiresAt: session.ExpiresAt,
		},
		HasAPIKey: hasKey,
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	session := sessionFromContext(r.Context())
	if err := s.sessions.Delete(r.Context(), session.ID); err != nil {
		s.logger.Error("logout failed", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "退出失败")
		return
	}
	s.clearSessionCookie(w, r)
	w.WriteHeader(http.StatusNoContent)
}

type passwordRequest struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

func (s *Server) handlePassword(w http.ResponseWriter, r *http.Request) {
	var request passwordRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "请求格式不正确")
		return
	}
	if err := auth.ValidatePassword(request.NewPassword); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_password", "新密码至少 8 位")
		return
	}
	user := userFromContext(r.Context())
	valid, err := s.accounts.VerifyPassword(r.Context(), user.ID, request.CurrentPassword)
	if err != nil {
		s.logger.Error("verify current password failed", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "修改密码失败")
		return
	}
	if !valid {
		writeAPIError(w, http.StatusUnauthorized, "invalid_credentials", "当前密码错误")
		return
	}
	hash, err := auth.HashPassword(request.NewPassword)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_password", "新密码不符合要求")
		return
	}
	if err := s.accounts.SetPassword(r.Context(), user.ID, hash); err != nil {
		s.logger.Error("set password failed", "error", err)
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "修改密码失败")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) hasAPIKey(ctx context.Context, userID string) (bool, error) {
	var present bool
	err := s.db.QueryRowContext(
		ctx,
		`SELECT COALESCE(api_key_ciphertext IS NOT NULL, 0) FROM user_settings WHERE user_id = ?`,
		userID,
	).Scan(&present)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return present, nil
}

func toUserJSON(user *account.User) userJSON {
	groups := make([]string, 0, len(user.Groups))
	for _, group := range user.Groups {
		groups = append(groups, string(group))
	}
	return userJSON{
		ID:        user.ID,
		Username:  user.Username,
		Groups:    groups,
		Disabled:  user.Disabled,
		CreatedAt: user.CreatedAt,
		UpdatedAt: user.UpdatedAt,
	}
}

type contextKey int

const (
	userContextKey contextKey = iota
	sessionContextKey
)

func userFromContext(ctx context.Context) *account.User {
	user, _ := ctx.Value(userContextKey).(*account.User)
	return user
}

func sessionFromContext(ctx context.Context) *auth.Session {
	session, _ := ctx.Value(sessionContextKey).(*auth.Session)
	return session
}

func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(auth.CookieName)
		if err != nil || cookie.Value == "" {
			writeAPIError(w, http.StatusUnauthorized, "unauthorized", "请先登录")
			return
		}
		session, err := s.sessions.GetByTokenHash(r.Context(), auth.HashToken(cookie.Value))
		if err != nil {
			writeAPIError(w, http.StatusUnauthorized, "unauthorized", "登录已失效")
			return
		}
		if session.ExpiresAt <= s.now().UnixMilli() {
			_ = s.sessions.Delete(r.Context(), session.ID)
			s.clearSessionCookie(w, r)
			writeAPIError(w, http.StatusUnauthorized, "unauthorized", "登录已过期")
			return
		}
		user, err := s.accounts.GetByID(r.Context(), session.UserID)
		if err != nil || user.Disabled {
			writeAPIError(w, http.StatusUnauthorized, "unauthorized", "登录已失效")
			return
		}
		ctx := context.WithValue(r.Context(), userContextKey, user)
		ctx = context.WithValue(ctx, sessionContextKey, session)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) withCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		session := sessionFromContext(r.Context())
		if session == nil {
			writeAPIError(w, http.StatusUnauthorized, "unauthorized", "请先登录")
			return
		}
		token := r.Header.Get("X-CSRF-Token")
		if subtle.ConstantTimeCompare([]byte(token), []byte(session.CSRF)) != 1 {
			writeAPIError(w, http.StatusForbidden, "invalid_csrf", "CSRF 校验失败")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, token string, expiresAt int64) {
	expires := time.UnixMilli(expiresAt)
	http.SetCookie(w, &http.Cookie{
		Name:     auth.CookieName,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		MaxAge:   int(time.Until(expires).Seconds()),
		HttpOnly: true,
		Secure:   s.secureCookie(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(1, 0),
		HttpOnly: true,
		Secure:   s.secureCookie(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func (s *Server) secureCookie(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if s.cfg.PublicURL == "" {
		return false
	}
	parsed, err := url.Parse(s.cfg.PublicURL)
	return err == nil && parsed.Scheme == "https"
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'; base-uri 'none'")
		next.ServeHTTP(w, r)
	})
}

func (s *Server) recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logger.Error("panic serving request", "panic", recovered, "path", r.URL.Path)
				writeAPIError(w, http.StatusInternalServerError, "internal_error", "服务器内部错误")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

type apiErrorBody struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	var body apiErrorBody
	body.Error.Code = code
	body.Error.Message = message
	writeJSON(w, status, body)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	return decoder.Decode(dst)
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Host, r.Host)
}

type loginLimiter struct {
	mu       sync.Mutex
	attempts map[string]loginAttempt
	max      int
	window   time.Duration
}

type loginAttempt struct {
	count   int
	resetAt time.Time
}

func newLoginLimiter(max int, window time.Duration) *loginLimiter {
	return &loginLimiter{
		attempts: make(map[string]loginAttempt),
		max:      max,
		window:   window,
	}
}

func (l *loginLimiter) Allow(key string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	attempt, ok := l.attempts[key]
	if !ok || now.After(attempt.resetAt) {
		return true, 0
	}
	if attempt.count >= l.max {
		return false, attempt.resetAt.Sub(now)
	}
	return true, 0
}

func (l *loginLimiter) Fail(key string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	attempt, ok := l.attempts[key]
	if !ok || now.After(attempt.resetAt) {
		l.attempts[key] = loginAttempt{count: 1, resetAt: now.Add(l.window)}
	} else {
		attempt.count++
		l.attempts[key] = attempt
	}
	if len(l.attempts) > 1000 {
		for item, value := range l.attempts {
			if now.After(value.resetAt) {
				delete(l.attempts, item)
			}
		}
	}
}

func (l *loginLimiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, key)
}
