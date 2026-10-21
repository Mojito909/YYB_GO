package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"yyb_go/internal/notify"
	"yyb_go/internal/store"
)

const barkSendTimeout = 10 * time.Second

type barkConfigRequest struct {
	Ref string `json:"ref"`
	Key string `json:"key"`
}

// handleAccountBark 查询或保存账号的 Bark device key。
func (a *App) handleAccountBark(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/accounts/bark" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		acc, ok := a.resolveAccountFromQuery(w, r)
		if !ok {
			return
		}
		writeJSON(w, http.StatusOK, barkStatus(acc, a.notifier.Server()))
	case http.MethodPost:
		var body barkConfigRequest
		if err := decodeOptionalJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		key, err := parseBarkInput(body.Key, a.notifier.Server())
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		acc, ok := a.resolveAccountRef(w, r, body.Ref)
		if !ok {
			return
		}
		if err := a.db.SetAccountBark(r.Context(), acc.ID, key); err != nil {
			writeInternalError(w, err)
			return
		}
		acc.BarkKey = key
		writeJSON(w, http.StatusOK, barkStatus(acc, a.notifier.Server()))
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleAccountBarkTest 发送一条测试通知；请求里带 key 时优先使用它。
func (a *App) handleAccountBarkTest(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/accounts/bark/test" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body barkConfigRequest
	if err := decodeOptionalJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	key, err := parseBarkInput(body.Key, a.notifier.Server())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	acc, ok := a.resolveAccountRef(w, r, body.Ref)
	if !ok {
		return
	}
	if key == "" {
		key = acc.BarkKey
	}
	if key == "" {
		writeError(w, http.StatusBadRequest, "请先填写 Bark device key")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), barkSendTimeout)
	defer cancel()
	if err := a.notifier.Send(ctx, key, "YYB Go 测试通知", "如果你收到这条消息，说明 Bark 通知配置可用。"); err != nil {
		if errors.Is(err, notify.ErrDeviceNotRegistered) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf(
				"Bark 服务中不存在该 device key，请确认填写的是 Bark App 中的 device key，并且推送服务地址（%s）与 App 中一致",
				a.notifier.Server()))
			return
		}
		writeError(w, http.StatusBadGateway, "bark push failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sent": true})
}

func barkStatus(acc *store.WechatAccount, server string) map[string]any {
	return map[string]any{
		"account_id": acc.ID,
		"enabled":    acc.BarkKey != "",
		"key_hint":   maskBarkKey(acc.BarkKey),
		"server":     server,
	}
}

func maskBarkKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 4 {
		return "****"
	}
	return "****" + key[len(key)-4:]
}

// parseBarkInput 接受 device key 或 Bark 推送 URL（形如 https://api.day.app/<key>/）。
// URL 的服务地址必须与当前配置的 Bark 服务一致。
func parseBarkInput(input, server string) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", nil
	}
	if strings.Contains(input, "://") {
		u, err := url.Parse(input)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return "", fmt.Errorf("Bark 推送 URL 格式不正确，请使用 https://<服务地址>/<device key>")
		}
		base, err := url.Parse(server)
		if err != nil || base.Host == "" {
			return "", fmt.Errorf("当前 Bark 服务地址无效：%s", server)
		}
		basePath := strings.TrimRight(base.Path, "/")
		if u.Scheme != base.Scheme || u.Host != base.Host || !strings.HasPrefix(u.Path, basePath+"/") {
			origin := u.Scheme + "://" + u.Host
			return "", fmt.Errorf("该 URL 指向 %s，与当前推送服务 %s 不一致；请把 YYB_BARK_SERVER 设置为 %s 并重启", origin, server, origin)
		}
		key := strings.Trim(strings.TrimPrefix(u.Path, basePath), "/")
		if key == "" || strings.Contains(key, "/") {
			return "", fmt.Errorf("Bark 推送 URL 中缺少 device key")
		}
		return normalizeBarkKey(key)
	}
	return normalizeBarkKey(input)
}

// normalizeBarkKey 清理并校验 device key；空字符串表示关闭通知。
func normalizeBarkKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", nil
	}
	if len(key) > 128 {
		return "", fmt.Errorf("bark key too long")
	}
	if strings.ContainsAny(key, " \t\r\n") {
		return "", fmt.Errorf("bark key must not contain whitespace")
	}
	return key, nil
}

// notifyAccountExpired 为失效账号领取一次通知机会并异步推送。
// 账号恢复 alive 后会重置标记，下一次失效可再次通知。
func (a *App) notifyAccountExpired(acc *store.WechatAccount) {
	if a.notifier == nil || acc.BarkKey == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	claimed, err := a.db.ClaimAccountExpiredNotification(ctx, acc.ID)
	cancel()
	if err != nil {
		log.Printf("[bark] 领取失效通知失败: %v", err)
		return
	}
	if !claimed {
		return
	}
	accountID := acc.ID
	key := acc.BarkKey
	name := accountDisplayName(acc)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), barkSendTimeout)
		defer cancel()
		body := fmt.Sprintf("账号「%s」登录已失效，请打开管理后台重新扫码。", name)
		if err := a.notifier.Send(ctx, key, "YYB Go 账号失效", body); err != nil {
			log.Printf("[bark] 账号 %d 通知发送失败: %v", accountID, err)
		}
	}()
}

// notifyAccountRecovered 在账号从 expired 恢复为 alive 时异步推送一次恢复通知。
// 转换本身由 SetAccountAlive 原子去重，这里无需再领取通知标记。
func (a *App) notifyAccountRecovered(acc *store.WechatAccount) {
	if a.notifier == nil || acc.BarkKey == "" {
		return
	}
	accountID := acc.ID
	key := acc.BarkKey
	name := accountDisplayName(acc)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), barkSendTimeout)
		defer cancel()
		body := fmt.Sprintf("账号「%s」已重新可用，无需处理。", name)
		if err := a.notifier.Send(ctx, key, "YYB Go 账号恢复", body); err != nil {
			log.Printf("[bark] 账号 %d 恢复通知发送失败: %v", accountID, err)
		}
	}()
}

func accountDisplayName(acc *store.WechatAccount) string {
	if acc.Nickname != nil && *acc.Nickname != "" {
		return *acc.Nickname
	}
	if acc.Alias != nil && *acc.Alias != "" {
		return *acc.Alias
	}
	if acc.OpenID != "" {
		return acc.OpenID
	}
	return "未命名账号"
}
