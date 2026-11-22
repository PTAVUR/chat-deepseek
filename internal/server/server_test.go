package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"

	"gitlab.com/elegantgx/chat-deepseek/internal/account"
	"gitlab.com/elegantgx/chat-deepseek/internal/auth"
	"gitlab.com/elegantgx/chat-deepseek/internal/config"
	"gitlab.com/elegantgx/chat-deepseek/internal/database"
	"gitlab.com/elegantgx/chat-deepseek/internal/filestore"
	"gitlab.com/elegantgx/chat-deepseek/internal/secret"
)

func newTestServer(t *testing.T) (*Server, *httptest.Server, *http.Client) {
	t.Helper()
	return newTestServerAt(t, t.TempDir())
}

func newTestServerAt(t *testing.T, dir string) (*Server, *httptest.Server, *http.Client) {
	t.Helper()
	db, err := database.Open(dir)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	key, err := secret.LoadOrCreate(dir)
	if err != nil {
		t.Fatalf("load key: %v", err)
	}
	files, err := filestore.NewStore(db, dir)
	if err != nil {
		t.Fatalf("create file store: %v", err)
	}
	hash, err := auth.HashPassword("password123")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if _, err := account.NewStore(db).Create(
		context.Background(),
		"owner",
		hash,
		[]account.Group{account.GroupUser, account.GroupOwner},
	); err != nil {
		t.Fatalf("create owner: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := New(config.Default(), db, logger, key, files)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	return srv, ts, &http.Client{Jar: jar}
}

func requestJSON(
	t *testing.T,
	client *http.Client,
	method string,
	url string,
	body any,
	csrf string,
) (int, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		reader = bytes.NewReader(data)
	}
	request, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if csrf != "" {
		request.Header.Set("X-CSRF-Token", csrf)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return response.StatusCode, data
}

func login(t *testing.T, client *http.Client, baseURL string) (userJSON, string) {
	t.Helper()
	status, body := requestJSON(t, client, http.MethodPost, baseURL+"/api/v1/auth/login", map[string]string{
		"username": "owner",
		"password": "password123",
	}, "")
	if status != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", status, body)
	}
	var response sessionResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	if response.CSRF == "" {
		t.Fatal("empty csrf")
	}
	return response.User, response.CSRF
}

func TestHealthAndLoginFlow(t *testing.T) {
	_, ts, client := newTestServer(t)

	status, _ := requestJSON(t, client, http.MethodGet, ts.URL+"/healthz", nil, "")
	if status != http.StatusOK {
		t.Fatalf("health status = %d", status)
	}
	status, _ = requestJSON(t, client, http.MethodGet, ts.URL+"/api/v1/auth/me", nil, "")
	if status != http.StatusUnauthorized {
		t.Fatalf("unauthenticated me status = %d", status)
	}
	status, _ = requestJSON(t, client, http.MethodPost, ts.URL+"/api/v1/auth/login", map[string]string{
		"username": "owner",
		"password": "wrong-password",
	}, "")
	if status != http.StatusUnauthorized {
		t.Fatalf("wrong login status = %d", status)
	}
	user, csrf := login(t, client, ts.URL)
	if user.Username != "owner" || len(user.Groups) != 2 {
		t.Fatalf("login user = %+v", user)
	}
	status, body := requestJSON(t, client, http.MethodGet, ts.URL+"/api/v1/auth/me", nil, "")
	if status != http.StatusOK {
		t.Fatalf("me status = %d, body = %s", status, body)
	}
	var me meResponse
	if err := json.Unmarshal(body, &me); err != nil {
		t.Fatalf("decode me: %v", err)
	}
	if me.User.Username != "owner" || me.CSRF != csrf || me.HasAPIKey {
		t.Fatalf("me = %+v", me)
	}

	status, _ = requestJSON(t, client, http.MethodPost, ts.URL+"/api/v1/auth/logout", nil, "")
	if status != http.StatusForbidden {
		t.Fatalf("logout without csrf status = %d", status)
	}
	status, _ = requestJSON(t, client, http.MethodPost, ts.URL+"/api/v1/auth/logout", nil, csrf)
	if status != http.StatusNoContent {
		t.Fatalf("logout status = %d", status)
	}
	status, _ = requestJSON(t, client, http.MethodGet, ts.URL+"/api/v1/auth/me", nil, "")
	if status != http.StatusUnauthorized {
		t.Fatalf("me after logout status = %d", status)
	}
}

func TestPasswordChange(t *testing.T) {
	_, ts, client := newTestServer(t)
	_, csrf := login(t, client, ts.URL)
	status, body := requestJSON(t, client, http.MethodPost, ts.URL+"/api/v1/auth/password", map[string]string{
		"currentPassword": "password123",
		"newPassword":     "new-password123",
	}, csrf)
	if status != http.StatusNoContent {
		t.Fatalf("change password status = %d, body = %s", status, body)
	}
	status, _ = requestJSON(t, client, http.MethodPost, ts.URL+"/api/v1/auth/login", map[string]string{
		"username": "owner",
		"password": "new-password123",
	}, "")
	if status != http.StatusOK {
		t.Fatalf("login with new password status = %d", status)
	}
}

func TestDisabledUserCannotLogin(t *testing.T) {
	srv, ts, client := newTestServer(t)
	hash, err := auth.HashPassword("password123")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	user, err := account.NewStore(srv.db).Create(
		context.Background(),
		"alice",
		hash,
		[]account.Group{account.GroupUser},
	)
	if err != nil {
		t.Fatalf("create alice: %v", err)
	}
	if err := account.NewStore(srv.db).SetDisabled(context.Background(), user.ID, true); err != nil {
		t.Fatalf("disable alice: %v", err)
	}
	status, _ := requestJSON(t, client, http.MethodPost, ts.URL+"/api/v1/auth/login", map[string]string{
		"username": "alice",
		"password": "password123",
	}, "")
	if status != http.StatusForbidden {
		t.Fatalf("disabled login status = %d", status)
	}
}

func TestUsernameAndAvatarUpdate(t *testing.T) {
	srv, ts, client := newTestServer(t)
	_, csrf := login(t, client, ts.URL)

	status, body := requestJSON(t, client, http.MethodPut, ts.URL+"/api/v1/auth/username", map[string]string{
		"username": "gx",
	}, csrf)
	if status != http.StatusNoContent {
		t.Fatalf("update username status = %d, body = %s", status, body)
	}

	status, body = requestJSON(t, client, http.MethodGet, ts.URL+"/api/v1/auth/me", nil, "")
	if status != http.StatusOK {
		t.Fatalf("me status = %d, body = %s", status, body)
	}
	var me meResponse
	if err := json.Unmarshal(body, &me); err != nil {
		t.Fatalf("decode me: %v", err)
	}
	if me.User.Username != "gx" {
		t.Fatalf("username = %q, want gx", me.User.Username)
	}

	hash, err := auth.HashPassword("password123")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if _, err := account.NewStore(srv.db).Create(
		context.Background(),
		"taken",
		hash,
		[]account.Group{account.GroupUser},
	); err != nil {
		t.Fatalf("create taken user: %v", err)
	}
	status, _ = requestJSON(t, client, http.MethodPut, ts.URL+"/api/v1/auth/username", map[string]string{
		"username": "taken",
	}, csrf)
	if status != http.StatusConflict {
		t.Fatalf("duplicate username status = %d", status)
	}
	status, _ = requestJSON(t, client, http.MethodPut, ts.URL+"/api/v1/auth/username", map[string]string{
		"username": "bad name",
	}, csrf)
	if status != http.StatusBadRequest {
		t.Fatalf("invalid username status = %d", status)
	}

	const png = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
	avatar := "data:image/png;base64," + png
	status, body = requestJSON(t, client, http.MethodPut, ts.URL+"/api/v1/auth/avatar", map[string]string{
		"avatar": avatar,
	}, csrf)
	if status != http.StatusNoContent {
		t.Fatalf("update avatar status = %d, body = %s", status, body)
	}
	status, body = requestJSON(t, client, http.MethodGet, ts.URL+"/api/v1/auth/me", nil, "")
	if status != http.StatusOK {
		t.Fatalf("me status = %d, body = %s", status, body)
	}
	if err := json.Unmarshal(body, &me); err != nil {
		t.Fatalf("decode me: %v", err)
	}
	if me.User.Avatar != avatar {
		t.Fatalf("avatar was not returned")
	}
	status, _ = requestJSON(t, client, http.MethodPut, ts.URL+"/api/v1/auth/avatar", map[string]string{
		"avatar": "not-a-data-url",
	}, csrf)
	if status != http.StatusBadRequest {
		t.Fatalf("invalid avatar status = %d", status)
	}
}
