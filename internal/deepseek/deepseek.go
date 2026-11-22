// Package deepseek 封装服务端调用 DeepSeek API 的 HTTP 细节。
package deepseek

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// DefaultBaseURL 是 DeepSeek API 的默认源地址。
	DefaultBaseURL = "https://api.deepseek.com"
	filesBeta      = "files-api-2025-04-14"
	maxErrorBody   = 1 << 20
)

// APIError 表示 DeepSeek 返回非 2xx；Body 已限制大小，调用方可以直接转给前端。
type APIError struct {
	StatusCode int
	Body       []byte
}

func (e *APIError) Error() string {
	return fmt.Sprintf("deepseek: status %d: %s", e.StatusCode, strings.TrimSpace(string(e.Body)))
}

// Message 是从错误响应中提取的简短说明。
func (e *APIError) Message() string {
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(e.Body, &body) == nil && body.Error.Message != "" {
		return body.Error.Message
	}
	return http.StatusText(e.StatusCode)
}

// Client 持有 API 地址与 HTTP 客户端；不保存 API Key。
type Client struct {
	baseURL string
	http    *http.Client
}

// New 创建客户端；httpClient 为 nil 时使用适合流式响应的默认配置。
func New(baseURL string, httpClient *http.Client) *Client {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if httpClient == nil {
		httpClient = &http.Client{
			Transport: &http.Transport{
				Proxy: http.ProxyFromEnvironment,
				DialContext: (&net.Dialer{
					Timeout:   10 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
				ForceAttemptHTTP2:     true,
				MaxIdleConns:          20,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ResponseHeaderTimeout: 120 * time.Second,
			},
		}
	}
	return &Client{baseURL: baseURL, http: httpClient}
}

// Models 用 GET /models 校验 API Key。
func (c *Client) Models(ctx context.Context, apiKey string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/models", nil)
	if err != nil {
		return fmt.Errorf("build models request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+apiKey)
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("request models: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return readAPIError(response)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxErrorBody))
	return nil
}

// UploadFile 上传附件并返回 file_id；purpose 为空时按图片处理，不传该字段。
func (c *Client) UploadFile(
	ctx context.Context,
	apiKey, name, purpose string,
	src io.Reader,
) (string, error) {
	reader, writer := io.Pipe()
	form := multipart.NewWriter(writer)
	go func() {
		var err error
		if purpose != "" {
			err = form.WriteField("purpose", purpose)
		}
		if err == nil {
			var part io.Writer
			part, err = form.CreateFormFile("file", name)
			if err == nil {
				_, err = io.Copy(part, src)
			}
		}
		if closeErr := form.Close(); err == nil {
			err = closeErr
		}
		_ = writer.CloseWithError(err)
	}()

	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.baseURL+"/anthropic/v1/files",
		reader,
	)
	if err != nil {
		_ = reader.Close()
		return "", fmt.Errorf("build upload request: %w", err)
	}
	request.Header.Set("Content-Type", form.FormDataContentType())
	request.Header.Set("x-api-key", apiKey)
	request.Header.Set("anthropic-beta", filesBeta)
	response, err := c.http.Do(request)
	if err != nil {
		_ = reader.Close()
		return "", fmt.Errorf("upload file: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_ = reader.Close()
		return "", readAPIError(response)
	}
	var file struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, maxErrorBody)).Decode(&file); err != nil {
		return "", fmt.Errorf("decode upload response: %w", err)
	}
	if file.ID == "" {
		return "", fmt.Errorf("upload response has no file id")
	}
	return file.ID, nil
}

// DeleteFile 删除远端文件。
func (c *Client) DeleteFile(ctx context.Context, apiKey, fileID string) error {
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodDelete,
		c.baseURL+"/anthropic/v1/files/"+url.PathEscape(fileID),
		nil,
	)
	if err != nil {
		return fmt.Errorf("build delete file request: %w", err)
	}
	request.Header.Set("x-api-key", apiKey)
	request.Header.Set("anthropic-beta", filesBeta)
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("delete file: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return readAPIError(response)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, maxErrorBody))
	return nil
}

// Messages 发起流式消息请求；调用方负责关闭响应体并转发 SSE。
func (c *Client) Messages(
	ctx context.Context,
	apiKey string,
	body []byte,
	hasFiles bool,
) (*http.Response, error) {
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.baseURL+"/anthropic/v1/messages",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, fmt.Errorf("build messages request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("x-api-key", apiKey)
	if hasFiles {
		request.Header.Set("anthropic-beta", filesBeta)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("request messages: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer func() { _ = response.Body.Close() }()
		return nil, readAPIError(response)
	}
	return response, nil
}

// ASR 转发语音识别请求。
func (c *Client) ASR(
	ctx context.Context,
	apiKey, contentType string,
	body io.Reader,
) (*http.Response, error) {
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.baseURL+"/v1/tools/asr",
		body,
	)
	if err != nil {
		return nil, fmt.Errorf("build asr request: %w", err)
	}
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("x-api-key", apiKey)
	return c.doBinary(request, "asr")
}

// TTS 转发语音合成请求，返回裸 PCM 响应。
func (c *Client) TTS(ctx context.Context, apiKey string, body []byte) (*http.Response, error) {
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.baseURL+"/v1/tools/tts",
		strings.NewReader(string(body)),
	)
	if err != nil {
		return nil, fmt.Errorf("build tts request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("x-api-key", apiKey)
	return c.doBinary(request, "tts")
}

func (c *Client) doBinary(request *http.Request, action string) (*http.Response, error) {
	response, err := c.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%s request: %w", action, err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer func() { _ = response.Body.Close() }()
		return nil, readAPIError(response)
	}
	return response, nil
}

func readAPIError(response *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(response.Body, maxErrorBody))
	return &APIError{StatusCode: response.StatusCode, Body: body}
}
