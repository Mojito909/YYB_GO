package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultServer 是 Bark 官方推送服务地址。
const DefaultServer = "https://api.day.app"

// Bark 通过 Bark 服务发送 iOS 推送通知。
type Bark struct {
	pushURL string
	client  *http.Client
}

// NewBark 创建 Bark 客户端；server 为空时使用官方服务。
func NewBark(server string, timeout time.Duration) (*Bark, error) {
	server = strings.TrimSpace(server)
	if server == "" {
		server = DefaultServer
	}
	u, err := url.Parse(server)
	if err != nil {
		return nil, fmt.Errorf("invalid bark server: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("bark server must be an http(s) URL")
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &Bark{
		pushURL: strings.TrimRight(u.String(), "/") + "/push",
		client:  &http.Client{Timeout: timeout},
	}, nil
}

type pushPayload struct {
	DeviceKey string `json:"device_key"`
	Title     string `json:"title"`
	Body      string `json:"body"`
	Group     string `json:"group,omitempty"`
}

// Send 推送一条通知。key 为 Bark device key。
func (b *Bark) Send(ctx context.Context, key, title, body string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("bark device key is empty")
	}
	raw, err := json.Marshal(pushPayload{
		DeviceKey: key,
		Title:     title,
		Body:      body,
		Group:     "YYB Go",
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.pushURL, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := b.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("bark HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	var result struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(data, &result) == nil && result.Code != 0 && result.Code != http.StatusOK {
		return fmt.Errorf("bark code=%d message=%s", result.Code, strings.TrimSpace(result.Message))
	}
	return nil
}
