package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/elegantgx/chat-deepseek/internal/filestore"
)

func chatWithFiles(id string, fileIDs ...string) map[string]any {
	files := make([]any, 0, len(fileIDs))
	for _, fileID := range fileIDs {
		files = append(files, map[string]any{"id": fileID, "name": "note.txt", "isImage": false})
	}
	return map[string]any{
		"session": map[string]any{
			"id":        id,
			"title":     id,
			"createdAt": 1,
			"updatedAt": 1,
			"messages": []any{
				map[string]any{
					"id":      "message-1",
					"role":    "user",
					"content": "hello",
					"files":   files,
				},
			},
		},
	}
}

func uploadTestFile(t *testing.T, client *http.Client, baseURL, csrf, name string) filestore.File {
	t.Helper()
	status, body := uploadMultipart(
		t,
		client,
		baseURL+"/api/v1/files",
		csrf,
		name,
		"text/plain",
		"user_data",
		false,
		[]byte("hello"),
	)
	if status != http.StatusCreated {
		t.Fatalf("upload %s status = %d, body = %s", name, status, body)
	}
	var file filestore.File
	if err := json.Unmarshal(body, &file); err != nil {
		t.Fatalf("decode uploaded %s: %v", name, err)
	}
	return file
}

func attachmentRowCount(t *testing.T, srv *Server, fileID string) int {
	t.Helper()
	var count int
	if err := srv.db.QueryRow(`SELECT COUNT(*) FROM files WHERE id = ?`, fileID).Scan(&count); err != nil {
		t.Fatalf("count attachment %s: %v", fileID, err)
	}
	return count
}

func assertAttachmentExists(t *testing.T, client *http.Client, srv *Server, baseURL, dir, userID, fileID string) {
	t.Helper()
	response, err := client.Get(baseURL + "/api/v1/files/" + fileID)
	if err != nil {
		t.Fatalf("get attachment %s: %v", fileID, err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("attachment %s status = %d", fileID, response.StatusCode)
	}
	if count := attachmentRowCount(t, srv, fileID); count != 1 {
		t.Fatalf("attachment %s rows = %d", fileID, count)
	}
	if _, err := os.Stat(filepath.Join(dir, "files", userID, fileID)); err != nil {
		t.Fatalf("attachment %s missing on disk: %v", fileID, err)
	}
}

func assertAttachmentGone(t *testing.T, client *http.Client, srv *Server, baseURL, dir, userID, fileID string) {
	t.Helper()
	response, err := client.Get(baseURL + "/api/v1/files/" + fileID)
	if err != nil {
		t.Fatalf("get attachment %s: %v", fileID, err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("attachment %s status = %d", fileID, response.StatusCode)
	}
	if count := attachmentRowCount(t, srv, fileID); count != 0 {
		t.Fatalf("attachment %s rows = %d", fileID, count)
	}
	if _, err := os.Stat(filepath.Join(dir, "files", userID, fileID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("attachment %s still on disk: %v", fileID, err)
	}
}

func TestDeleteChatDeletesUnreferencedAttachments(t *testing.T) {
	dir := t.TempDir()
	srv, ts, client := newTestServerAt(t, dir)
	user, csrf := login(t, client, ts.URL)
	only := uploadTestFile(t, client, ts.URL, csrf, "only.txt")
	shared := uploadTestFile(t, client, ts.URL, csrf, "shared.txt")

	status, body := requestJSON(
		t,
		client,
		http.MethodPost,
		ts.URL+"/api/v1/chats",
		chatWithFiles("session-a", only.ID, shared.ID),
		csrf,
	)
	if status != http.StatusCreated {
		t.Fatalf("create session-a status = %d, body = %s", status, body)
	}
	status, body = requestJSON(
		t,
		client,
		http.MethodPost,
		ts.URL+"/api/v1/chats",
		chatWithFiles("session-b", shared.ID),
		csrf,
	)
	if status != http.StatusCreated {
		t.Fatalf("create session-b status = %d, body = %s", status, body)
	}

	status, body = requestJSON(t, client, http.MethodDelete, ts.URL+"/api/v1/chats/session-a", nil, csrf)
	if status != http.StatusNoContent {
		t.Fatalf("delete session-a status = %d, body = %s", status, body)
	}
	assertAttachmentGone(t, client, srv, ts.URL, dir, user.ID, only.ID)
	// shared 仍被 session-b 引用，不能跟着 session-a 一起删。
	assertAttachmentExists(t, client, srv, ts.URL, dir, user.ID, shared.ID)

	status, body = requestJSON(t, client, http.MethodDelete, ts.URL+"/api/v1/chats/session-b", nil, csrf)
	if status != http.StatusNoContent {
		t.Fatalf("delete session-b status = %d, body = %s", status, body)
	}
	assertAttachmentGone(t, client, srv, ts.URL, dir, user.ID, shared.ID)
}

func TestDeleteAllFilesRemovesDiskAndMetadata(t *testing.T) {
	dir := t.TempDir()
	srv, ts, client := newTestServerAt(t, dir)
	user, csrf := login(t, client, ts.URL)
	first := uploadTestFile(t, client, ts.URL, csrf, "first.txt")
	second := uploadTestFile(t, client, ts.URL, csrf, "second.txt")

	status, body := requestJSON(t, client, http.MethodDelete, ts.URL+"/api/v1/files", nil, csrf)
	if status != http.StatusNoContent {
		t.Fatalf("delete all status = %d, body = %s", status, body)
	}
	assertAttachmentGone(t, client, srv, ts.URL, dir, user.ID, first.ID)
	assertAttachmentGone(t, client, srv, ts.URL, dir, user.ID, second.ID)
	if _, err := os.Stat(filepath.Join(dir, "files", user.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("user files directory still exists: %v", err)
	}
	var count int
	if err := srv.db.QueryRow(`SELECT COUNT(*) FROM files WHERE user_id = ?`, user.ID).Scan(&count); err != nil {
		t.Fatalf("count files: %v", err)
	}
	if count != 0 {
		t.Fatalf("attachment rows = %d", count)
	}
}
