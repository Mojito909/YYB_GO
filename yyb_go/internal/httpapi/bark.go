package httpapi

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

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
		writeJSON(w, http.StatusOK, barkStatus(acc))
	case http.MethodPost:
		var body barkConfigRequest
		if err := decodeOptionalJSON(r, &body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		key, err := normalizeBarkKey(body.Key)
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
		writeJSON(w, http.StatusOK, barkStatus(acc))
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
	key, err := normalizeBarkKey(body.Key)
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
		writeError(w, http.StatusBadGateway, "bark push failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sent": true})
}

func barkStatus(acc *store.WechatAccount) map[string]any {
	return map[string]any{
		"account_id": acc.ID,
		"enabled":    acc.BarkKey != "",
		"key_hint":   maskBarkKey(acc.BarkKey),
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
