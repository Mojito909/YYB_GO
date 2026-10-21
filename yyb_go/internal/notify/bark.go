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

// ErrDeviceNotRegistered 表示 Bark 服务中没有这个 device key 对应的设备。
// 常见原因是填了 API 令牌 / APNs token，或设备与服务端地址不匹配。
var ErrDeviceNotRegistered = errors.New("bark device key is not registered on this server")

// Bark 通过 Bark 服务发送 iOS 推送通知。
type Bark struct {
	baseURL string
	pushURL string
	client  *http.Client
}

// Server 返回当前使用的 Bark 服务地址（不含末尾斜杠和 /push）。
func (b *Bark) Server() string { return b.baseURL }

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
	baseURL := strings.TrimRight(u.String(), "/")
	return &Bark{
		baseURL: baseURL,
		pushURL: baseURL + "/push",
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
		return classifyBarkError(fmt.Sprintf("bark HTTP %d", resp.StatusCode), data)
	}
	var result struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(data, &result) == nil && result.Code != 0 && result.Code != http.StatusOK {
		return classifyBarkError(fmt.Sprintf("bark code=%d", result.Code), data)
	}
	return nil
}

// classifyBarkError 把 Bark 的“设备不存在”错误单独识别出来，便于上层给出可操作的提示。
func classifyBarkError(prefix string, data []byte) error {
	if strings.Contains(strings.ToLower(string(data)), "failed to get device token") {
		return ErrDeviceNotRegistered
	}
	return fmt.Errorf("%s: %s", prefix, strings.TrimSpace(string(data)))
}
