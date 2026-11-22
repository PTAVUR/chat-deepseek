package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestSettingsAPI(t *testing.T) {
	srv, ts, client := newTestServer(t)
	status, _ := requestJSON(t, client, http.MethodGet, ts.URL+"/api/v1/settings", nil, "")
	if status != http.StatusUnauthorized {
		t.Fatalf("unauthenticated settings status = %d", status)
	}

	_, csrf := login(t, client, ts.URL)
	status, body := requestJSON(t, client, http.MethodGet, ts.URL+"/api/v1/settings", nil, "")
	if status != http.StatusOK {
		t.Fatalf("get settings status = %d, body = %s", status, body)
	}
	var initial settingsResponse
	if err := json.Unmarshal(body, &initial); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if string(initial.Preferences) != `{}` || initial.HasAPIKey {
		t.Fatalf("initial settings = %+v", initial)
	}

	status, _ = requestJSON(t, client, http.MethodPut, ts.URL+"/api/v1/settings", map[string]any{
		"preferences": map[string]any{"locale": "zh-CN"},
	}, "")
	if status != http.StatusForbidden {
		t.Fatalf("settings without csrf status = %d", status)
	}
	status, body = requestJSON(t, client, http.MethodPut, ts.URL+"/api/v1/settings", map[string]any{
		"preferences": map[string]any{"locale": "zh-CN", "thinking": true},
	}, csrf)
	if status != http.StatusOK {
		t.Fatalf("update settings status = %d, body = %s", status, body)
	}
	var updated settingsResponse
	if err := json.Unmarshal(body, &updated); err != nil {
		t.Fatalf("decode updated settings: %v", err)
	}
	if string(updated.Preferences) != `{"locale":"zh-CN","thinking":true}` || updated.HasAPIKey {
		t.Fatalf("updated settings = %+v", updated)
	}

	status, body = requestJSON(t, client, http.MethodPut, ts.URL+"/api/v1/settings/api-key", map[string]string{
		"apiKey": "sk-test-secret",
	}, csrf)
	if status != http.StatusOK {
		t.Fatalf("set api key status = %d, body = %s", status, body)
	}
	if bytes.Contains(body, []byte("sk-test-secret")) {
		t.Fatal("set api key response contains plaintext")
	}
	var keyResponse map[string]bool
	if err := json.Unmarshal(body, &keyResponse); err != nil {
		t.Fatalf("decode key response: %v", err)
	}
	if !keyResponse["hasApiKey"] {
		t.Fatalf("key response = %v", keyResponse)
	}
	status, body = requestJSON(t, client, http.MethodGet, ts.URL+"/api/v1/auth/me", nil, "")
	if status != http.StatusOK {
		t.Fatalf("me status = %d", status)
	}
	var me meResponse
	if err := json.Unmarshal(body, &me); err != nil {
		t.Fatalf("decode me: %v", err)
	}
	if !me.HasAPIKey {
		t.Fatal("me hasApiKey = false")
	}

	status, body = requestJSON(t, client, http.MethodGet, ts.URL+"/api/v1/settings", nil, "")
	if status != http.StatusOK {
		t.Fatalf("get settings after key status = %d", status)
	}
	if strings.Contains(string(body), "sk-test-secret") {
		t.Fatal("settings response contains plaintext api key")
	}
	var withKey settingsResponse
	if err := json.Unmarshal(body, &withKey); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if !withKey.HasAPIKey || string(withKey.Preferences) != `{"locale":"zh-CN","thinking":true}` {
		t.Fatalf("settings after key = %+v", withKey)
	}

	var ciphertext []byte
	if err := srv.db.QueryRow(
		`SELECT api_key_ciphertext FROM user_settings`,
	).Scan(&ciphertext); err != nil {
		t.Fatalf("read stored ciphertext: %v", err)
	}
	if bytes.Contains(ciphertext, []byte("sk-test-secret")) {
		t.Fatal("stored ciphertext contains plaintext")
	}

	status, _ = requestJSON(t, client, http.MethodPut, ts.URL+"/api/v1/settings", map[string]any{
		"preferences": map[string]any{"apiKey": "sk-leak"},
	}, csrf)
	if status != http.StatusBadRequest {
		t.Fatalf("plaintext key settings status = %d", status)
	}

	status, body = requestJSON(t, client, http.MethodDelete, ts.URL+"/api/v1/settings/api-key", nil, csrf)
	if status != http.StatusOK {
		t.Fatalf("clear api key status = %d, body = %s", status, body)
	}
	if err := json.Unmarshal(body, &keyResponse); err != nil {
		t.Fatalf("decode clear response: %v", err)
	}
	if keyResponse["hasApiKey"] {
		t.Fatalf("clear response = %v", keyResponse)
	}
}

func TestChatsAPI(t *testing.T) {
	_, ts, client := newTestServer(t)
	_, csrf := login(t, client, ts.URL)
	session := map[string]any{
		"id":        "session-1",
		"title":     "Hello",
		"createdAt": 1000,
		"updatedAt": 2000,
		"messages": []any{
			map[string]any{"id": "m1", "role": "user", "content": "hi", "files": []any{}, "createdAt": 1000},
		},
	}

	status, _ := requestJSON(t, client, http.MethodPost, ts.URL+"/api/v1/chats", map[string]any{
		"session": session,
	}, "")
	if status != http.StatusForbidden {
		t.Fatalf("create without csrf status = %d", status)
	}
	status, body := requestJSON(t, client, http.MethodPost, ts.URL+"/api/v1/chats", map[string]any{
		"session": session,
	}, csrf)
	if status != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", status, body)
	}
	var created chatResponse
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.Version != 1 {
		t.Fatalf("created version = %d", created.Version)
	}

	status, body = requestJSON(t, client, http.MethodGet, ts.URL+"/api/v1/chats", nil, "")
	if status != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", status, body)
	}
	var list chatListResponse
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Chats) != 1 || list.Chats[0].ID != "session-1" {
		t.Fatalf("list = %+v", list)
	}

	status, body = requestJSON(t, client, http.MethodGet, ts.URL+"/api/v1/chats/session-1", nil, "")
	if status != http.StatusOK {
		t.Fatalf("get status = %d, body = %s", status, body)
	}
	var got chatResponse
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if got.Version != 1 || !bytes.Contains(got.Session, []byte(`"title":"Hello"`)) {
		t.Fatalf("got = %+v", got)
	}

	session["title"] = "Renamed"
	status, body = requestJSON(t, client, http.MethodPut, ts.URL+"/api/v1/chats/session-1", map[string]any{
		"session": session,
		"version": 1,
	}, csrf)
	if status != http.StatusOK {
		t.Fatalf("update status = %d, body = %s", status, body)
	}
	var updated chatResponse
	if err := json.Unmarshal(body, &updated); err != nil {
		t.Fatalf("decode update: %v", err)
	}
	if updated.Version != 2 {
		t.Fatalf("updated version = %d", updated.Version)
	}
	status, _ = requestJSON(t, client, http.MethodPut, ts.URL+"/api/v1/chats/session-1", map[string]any{
		"session": session,
		"version": 1,
	}, csrf)
	if status != http.StatusConflict {
		t.Fatalf("stale update status = %d", status)
	}

	status, _ = requestJSON(t, client, http.MethodDelete, ts.URL+"/api/v1/chats/session-1", nil, csrf)
	if status != http.StatusNoContent {
		t.Fatalf("delete status = %d", status)
	}
	status, _ = requestJSON(t, client, http.MethodGet, ts.URL+"/api/v1/chats/session-1", nil, "")
	if status != http.StatusNotFound {
		t.Fatalf("get deleted status = %d", status)
	}
	status, body = requestJSON(t, client, http.MethodGet, ts.URL+"/api/v1/chats?deleted=true", nil, "")
	if status != http.StatusOK {
		t.Fatalf("list deleted status = %d, body = %s", status, body)
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode deleted list: %v", err)
	}
	if len(list.Chats) != 1 {
		t.Fatalf("deleted list = %+v", list)
	}

	status, _ = requestJSON(t, client, http.MethodPost, ts.URL+"/api/v1/chats/session-1/restore", nil, csrf)
	if status != http.StatusNoContent {
		t.Fatalf("restore status = %d", status)
	}
	status, _ = requestJSON(t, client, http.MethodDelete, ts.URL+"/api/v1/chats/session-1", nil, csrf)
	if status != http.StatusNoContent {
		t.Fatalf("delete again status = %d", status)
	}
	status, _ = requestJSON(t, client, http.MethodDelete, ts.URL+"/api/v1/chats/session-1/purge", nil, csrf)
	if status != http.StatusNoContent {
		t.Fatalf("purge status = %d", status)
	}
}
