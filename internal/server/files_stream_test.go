package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"sync"
	"testing"

	"gitlab.com/elegantgx/chat-deepseek/internal/deepseek"
	"gitlab.com/elegantgx/chat-deepseek/internal/filestore"
)

func uploadMultipart(
	t *testing.T,
	client *http.Client,
	url, csrf, name, contentType, purpose string,
	isImage bool,
	data []byte,
) (int, []byte) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if purpose != "" {
		if err := writer.WriteField("purpose", purpose); err != nil {
			t.Fatalf("write purpose: %v", err)
		}
	}
	if isImage {
		if err := writer.WriteField("isImage", "true"); err != nil {
			t.Fatalf("write isImage: %v", err)
		}
	}
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, name))
	header.Set("Content-Type", contentType)
	part, err := writer.CreatePart(header)
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	request, err := http.NewRequest(http.MethodPost, url, &body)
	if err != nil {
		t.Fatalf("new upload request: %v", err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())
	if csrf != "" {
		request.Header.Set("X-CSRF-Token", csrf)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read upload response: %v", err)
	}
	return response.StatusCode, responseBody
}

func TestFileUploadDownloadDelete(t *testing.T) {
	_, ts, client := newTestServer(t)
	_, csrf := login(t, client, ts.URL)
	status, _ := uploadMultipart(t, client, ts.URL+"/api/v1/files", "", "note.txt", "text/plain", "user_data", false, []byte("hello"))
	if status != http.StatusForbidden {
		t.Fatalf("upload without csrf status = %d", status)
	}

	status, body := uploadMultipart(t, client, ts.URL+"/api/v1/files", csrf, "note.txt", "text/plain", "user_data", false, []byte("hello"))
	if status != http.StatusCreated {
		t.Fatalf("upload status = %d, body = %s", status, body)
	}
	var file filestore.File
	if err := json.Unmarshal(body, &file); err != nil {
		t.Fatalf("decode file: %v", err)
	}
	if file.ID == "" || file.Name != "note.txt" || file.Purpose != "user_data" {
		t.Fatalf("file = %+v", file)
	}

	request, err := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/files/"+file.ID, nil)
	if err != nil {
		t.Fatalf("new download request: %v", err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	downloaded, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || string(downloaded) != "hello" {
		t.Fatalf("download status = %d, body = %q", response.StatusCode, downloaded)
	}
	if !strings.Contains(response.Header.Get("Content-Disposition"), "attachment") {
		t.Fatalf("content disposition = %q", response.Header.Get("Content-Disposition"))
	}

	status, body = uploadMultipart(t, client, ts.URL+"/api/v1/files", csrf, "photo.png", "image/png", "", true, []byte("png"))
	if status != http.StatusCreated {
		t.Fatalf("image upload status = %d, body = %s", status, body)
	}
	var image filestore.File
	if err := json.Unmarshal(body, &image); err != nil {
		t.Fatalf("decode image: %v", err)
	}
	if !image.IsImage || image.Purpose != "" || image.MIMEType != "image/png" {
		t.Fatalf("image = %+v", image)
	}

	status, _ = requestJSON(t, client, http.MethodDelete, ts.URL+"/api/v1/files/"+file.ID, nil, csrf)
	if status != http.StatusNoContent {
		t.Fatalf("delete status = %d", status)
	}
	request, _ = http.NewRequest(http.MethodGet, ts.URL+"/api/v1/files/"+file.ID, nil)
	response, err = client.Do(request)
	if err != nil {
		t.Fatalf("download deleted: %v", err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("download deleted status = %d", response.StatusCode)
	}
}

func TestStreamProxy(t *testing.T) {
	srv, ts, client := newTestServer(t)
	user, csrf := login(t, client, ts.URL)

	var mu sync.Mutex
	var capturedKey, capturedBeta string
	var capturedBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/anthropic/v1/files":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("parse upload: %v", err)
				http.Error(w, "bad form", http.StatusBadRequest)
				return
			}
			if r.FormValue("purpose") != "user_data" {
				t.Errorf("purpose = %q", r.FormValue("purpose"))
			}
			file, _, err := r.FormFile("file")
			if err != nil {
				t.Errorf("form file: %v", err)
				http.Error(w, "missing file", http.StatusBadRequest)
				return
			}
			data, _ := io.ReadAll(file)
			_ = file.Close()
			if string(data) != "hello" {
				t.Errorf("uploaded data = %q", data)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"file-uploaded","type":"file"}`))
		case "/anthropic/v1/messages":
			body, _ := io.ReadAll(r.Body)
			mu.Lock()
			capturedKey = r.Header.Get("x-api-key")
			capturedBeta = r.Header.Get("anthropic-beta")
			capturedBody = body
			mu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"type\":\"message_start\"}\n\n"))
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			_, _ = w.Write([]byte("data: {\"type\":\"message_stop\"}\n\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	srv.deepseek = deepseek.New(upstream.URL, nil)

	status, body := requestJSON(t, client, http.MethodPost, ts.URL+"/api/v1/stream", map[string]any{
		"model":    "deepseek-flash",
		"messages": []any{},
	}, csrf)
	if status != http.StatusBadRequest || !strings.Contains(string(body), "api_key_missing") {
		t.Fatalf("missing key status = %d, body = %s", status, body)
	}

	status, body = requestJSON(t, client, http.MethodPut, ts.URL+"/api/v1/settings/api-key", map[string]string{
		"apiKey": "sk-test-secret",
	}, csrf)
	if status != http.StatusOK {
		t.Fatalf("set key status = %d, body = %s", status, body)
	}

	status, body = requestJSON(t, client, http.MethodPost, ts.URL+"/api/v1/stream", map[string]any{
		"model":    "deepseek-flash",
		"messages": []any{map[string]any{"role": "user", "content": "hi"}},
	}, csrf)
	if status != http.StatusOK {
		t.Fatalf("stream status = %d, body = %s", status, body)
	}
	if !strings.Contains(string(body), "message_stop") {
		t.Fatalf("stream body = %s", body)
	}
	mu.Lock()
	if capturedKey != "sk-test-secret" {
		t.Errorf("upstream key = %q", capturedKey)
	}
	if capturedBeta != "" {
		t.Errorf("unexpected beta = %q", capturedBeta)
	}
	var forwarded map[string]any
	if err := json.Unmarshal(capturedBody, &forwarded); err != nil {
		t.Errorf("forwarded body: %v", err)
	}
	if forwarded["stream"] != true {
		t.Errorf("stream field = %v", forwarded["stream"])
	}
	mu.Unlock()

	status, body = uploadMultipart(t, client, ts.URL+"/api/v1/files", csrf, "note.txt", "text/plain", "user_data", false, []byte("hello"))
	if status != http.StatusCreated {
		t.Fatalf("upload status = %d, body = %s", status, body)
	}
	var file filestore.File
	if err := json.Unmarshal(body, &file); err != nil {
		t.Fatalf("decode file: %v", err)
	}

	status, body = requestJSON(t, client, http.MethodPost, ts.URL+"/api/v1/stream", map[string]any{
		"model": "deepseek-flash",
		"messages": []any{map[string]any{
			"role": "user",
			"content": []any{map[string]any{
				"type":   "document",
				"source": map[string]any{"type": "file", "file_id": file.ID},
			}},
		}},
	}, csrf)
	if status != http.StatusOK {
		t.Fatalf("file stream status = %d, body = %s", status, body)
	}
	mu.Lock()
	if capturedBeta != "files-api-2025-04-14" {
		t.Errorf("file beta = %q", capturedBeta)
	}
	if !bytes.Contains(capturedBody, []byte(`"file_id":"file-uploaded"`)) {
		t.Errorf("forwarded body has no remote id: %s", capturedBody)
	}
	mu.Unlock()
	cached, err := srv.files.Get(context.Background(), user.ID, file.ID)
	if err != nil {
		t.Fatalf("get cached file: %v", err)
	}
	if cached.ServerID != "file-uploaded" {
		t.Fatalf("cached server id = %q", cached.ServerID)
	}
}

func TestStreamProxyPassesUpstreamError(t *testing.T) {
	srv, ts, client := newTestServer(t)
	_, csrf := login(t, client, ts.URL)
	status, body := requestJSON(t, client, http.MethodPut, ts.URL+"/api/v1/settings/api-key", map[string]string{
		"apiKey": "sk-test-secret",
	}, csrf)
	if status != http.StatusOK {
		t.Fatalf("set key status = %d, body = %s", status, body)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer upstream.Close()
	srv.deepseek = deepseek.New(upstream.URL, nil)

	status, body = requestJSON(t, client, http.MethodPost, ts.URL+"/api/v1/stream", map[string]any{
		"model":    "deepseek-flash",
		"messages": []any{},
	}, csrf)
	if status != http.StatusUnauthorized || !strings.Contains(string(body), "invalid api key") {
		t.Fatalf("upstream error status = %d, body = %s", status, body)
	}
}
