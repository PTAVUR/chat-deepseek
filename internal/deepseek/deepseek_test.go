package deepseek

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUploadMessagesAndDelete(t *testing.T) {
	var sawPurpose, sawFile, sawBeta, sawKey bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/anthropic/v1/files":
			sawKey = r.Header.Get("x-api-key") == "sk-test"
			sawBeta = r.Header.Get("anthropic-beta") == filesBeta
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("parse multipart: %v", err)
				http.Error(w, "bad form", http.StatusBadRequest)
				return
			}
			sawPurpose = r.FormValue("purpose") == "user_data"
			file, _, err := r.FormFile("file")
			if err != nil {
				t.Errorf("form file: %v", err)
				http.Error(w, "missing file", http.StatusBadRequest)
				return
			}
			defer func() { _ = file.Close() }()
			data, _ := io.ReadAll(file)
			sawFile = string(data) == "hello"
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"file-123","type":"file"}`))
		case "/anthropic/v1/messages":
			sawKey = r.Header.Get("x-api-key") == "sk-test"
			sawBeta = r.Header.Get("anthropic-beta") == filesBeta
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), `"stream":true`) {
				t.Errorf("body = %s", body)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"type\":\"message_stop\"}\n\n"))
		case "/anthropic/v1/files/file-123":
			if r.Method != http.MethodDelete {
				t.Errorf("delete method = %s", r.Method)
			}
			w.WriteHeader(http.StatusNoContent)
		case "/models":
			if r.Header.Get("Authorization") != "Bearer sk-test" {
				t.Errorf("models authorization = %q", r.Header.Get("Authorization"))
			}
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := New(server.URL, nil)
	ctx := context.Background()
	fileID, err := client.UploadFile(ctx, "sk-test", "notes.txt", "user_data", strings.NewReader("hello"))
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if fileID != "file-123" {
		t.Fatalf("file id = %q", fileID)
	}
	if !sawPurpose || !sawFile || !sawBeta || !sawKey {
		t.Fatalf("upload flags = purpose %v file %v beta %v key %v", sawPurpose, sawFile, sawBeta, sawKey)
	}

	response, err := client.Messages(ctx, "sk-test", []byte(`{"stream":true}`), true)
	if err != nil {
		t.Fatalf("messages: %v", err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if !strings.Contains(string(body), "message_stop") {
		t.Fatalf("stream body = %s", body)
	}

	if err := client.DeleteFile(ctx, "sk-test", "file-123"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := client.Models(ctx, "sk-test"); err != nil {
		t.Fatalf("models: %v", err)
	}
}

func TestAPIErrorIncludesMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer server.Close()

	client := New(server.URL, nil)
	_, err := client.UploadFile(context.Background(), "bad", "x.txt", "", strings.NewReader("x"))
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v", err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized || apiErr.Message() != "invalid api key" {
		t.Fatalf("api error = %+v, message = %q", apiErr, apiErr.Message())
	}
}
