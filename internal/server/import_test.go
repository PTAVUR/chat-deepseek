package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func importSession(id string, fileIDs ...string) map[string]any {
	session, _ := chatWithFiles(id, fileIDs...)["session"].(map[string]any)
	return session
}

func TestImportReplacesChatsAtomically(t *testing.T) {
	dir := t.TempDir()
	_, ts, client := newTestServerAt(t, dir)
	user, csrf := login(t, client, ts.URL)
	oldFile := uploadTestFile(t, client, ts.URL, csrf, "old.txt")
	newFile := uploadTestFile(t, client, ts.URL, csrf, "new.txt")

	status, body := requestJSON(
		t,
		client,
		http.MethodPost,
		ts.URL+"/api/v1/chats",
		chatWithFiles("old-session", oldFile.ID),
		csrf,
	)
	if status != http.StatusCreated {
		t.Fatalf("create old chat status = %d, body = %s", status, body)
	}

	// 重复会话 ID 必须在事务前失败，旧会话和旧附件不能被动过。
	status, body = requestJSON(t, client, http.MethodPost, ts.URL+"/api/v1/import", map[string]any{
		"sessions": []any{
			importSession("import-a", newFile.ID),
			importSession("import-a"),
		},
	}, csrf)
	if status != http.StatusBadRequest {
		t.Fatalf("invalid import status = %d, body = %s", status, body)
	}
	status, _ = requestJSON(t, client, http.MethodGet, ts.URL+"/api/v1/chats/old-session", nil, "")
	if status != http.StatusOK {
		t.Fatalf("old chat after failed import status = %d", status)
	}
	status, _ = requestJSON(t, client, http.MethodGet, ts.URL+"/api/v1/files/"+oldFile.ID, nil, "")
	if status != http.StatusOK {
		t.Fatalf("old file after failed import status = %d", status)
	}

	status, body = requestJSON(t, client, http.MethodPost, ts.URL+"/api/v1/import", map[string]any{
		"sessions": []any{
			importSession("import-a", newFile.ID),
			importSession("import-b"),
		},
	}, csrf)
	if status != http.StatusNoContent {
		t.Fatalf("import status = %d, body = %s", status, body)
	}

	status, body = requestJSON(t, client, http.MethodGet, ts.URL+"/api/v1/chats", nil, "")
	if status != http.StatusOK {
		t.Fatalf("list after import status = %d, body = %s", status, body)
	}
	var list chatListResponse
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatalf("decode list after import: %v", err)
	}
	if len(list.Chats) != 2 {
		t.Fatalf("chats after import = %+v", list)
	}
	status, _ = requestJSON(t, client, http.MethodGet, ts.URL+"/api/v1/chats/old-session", nil, "")
	if status != http.StatusNotFound {
		t.Fatalf("old chat after import status = %d", status)
	}
	status, _ = requestJSON(t, client, http.MethodGet, ts.URL+"/api/v1/files/"+oldFile.ID, nil, "")
	if status != http.StatusNotFound {
		t.Fatalf("old file after import status = %d", status)
	}
	if _, err := os.Stat(filepath.Join(dir, "files", user.ID, oldFile.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old file still on disk: %v", err)
	}
	status, _ = requestJSON(t, client, http.MethodGet, ts.URL+"/api/v1/files/"+newFile.ID, nil, "")
	if status != http.StatusOK {
		t.Fatalf("new file after import status = %d", status)
	}
	status, _ = requestJSON(t, client, http.MethodGet, ts.URL+"/api/v1/chats/import-a", nil, "")
	if status != http.StatusOK {
		t.Fatalf("imported chat status = %d", status)
	}
}
