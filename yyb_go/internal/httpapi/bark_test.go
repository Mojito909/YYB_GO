package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"yyb_go/internal/notify"
	"yyb_go/internal/protocol"
)

func newBarkTestServer(t *testing.T) (*httptest.Server, chan map[string]any) {
	t.Helper()
	received := make(chan map[string]any, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		_ = json.NewDecoder(r.Body).Decode(&payload)
		select {
		case received <- payload:
		default:
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"code":200,"message":"success"}`))
	}))
	t.Cleanup(server.Close)
	return server, received
}

func TestBarkConfigEndpoints(t *testing.T) {
	app, handler := newTestApp(t)
	server, received := newBarkTestServer(t)
	bark, err := notify.NewBark(server.URL, time.Second)
	if err != nil {
		t.Fatalf("NewBark() error = %v", err)
	}
	app.notifier = bark

	if _, err := app.db.UpsertAccount(context.Background(), "openid-bark", "buffer", nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("create account: %v", err)
	}
	cookie := loginForTest(t, handler, testAdminUser, testAdminPass)

	status := func() map[string]any {
		t.Helper()
		rec := authedRequest(handler, cookie, http.MethodGet, "/accounts/bark?ref=openid-bark", nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET /accounts/bark status = %d, body = %s", rec.Code, rec.Body.String())
		}
		var envelope struct {
			Data map[string]any `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
			t.Fatalf("decode bark status: %v", err)
		}
		return envelope.Data
	}

	if enabled, _ := status()["enabled"].(bool); enabled {
		t.Fatalf("bark should be disabled by default")
	}

	save := authedRequest(handler, cookie, http.MethodPost, "/accounts/bark",
		[]byte(`{"ref":"openid-bark","key":"device-key-1234"}`))
	if save.Code != http.StatusOK {
		t.Fatalf("POST /accounts/bark status = %d, body = %s", save.Code, save.Body.String())
	}
	info := status()
	if enabled, _ := info["enabled"].(bool); !enabled {
		t.Fatalf("bark should be enabled after save")
	}
	if info["key_hint"] != "****1234" {
		t.Fatalf("key_hint = %v, want ****1234", info["key_hint"])
	}
	if info["server"] != server.URL {
		t.Fatalf("server = %v, want %s", info["server"], server.URL)
	}

	testRec := authedRequest(handler, cookie, http.MethodPost, "/accounts/bark/test",
		[]byte(`{"ref":"openid-bark"}`))
	if testRec.Code != http.StatusOK {
		t.Fatalf("POST /accounts/bark/test status = %d, body = %s", testRec.Code, testRec.Body.String())
	}
	select {
	case payload := <-received:
		if payload["device_key"] != "device-key-1234" {
			t.Fatalf("device_key = %v", payload["device_key"])
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("bark server did not receive test notification")
	}

	clear := authedRequest(handler, cookie, http.MethodPost, "/accounts/bark",
		[]byte(`{"ref":"openid-bark","key":""}`))
	if clear.Code != http.StatusOK {
		t.Fatalf("clear bark status = %d, body = %s", clear.Code, clear.Body.String())
	}
	if enabled, _ := status()["enabled"].(bool); enabled {
		t.Fatalf("bark should be disabled after clearing key")
	}

	// 支持直接粘贴 Bark 推送 URL。
	urlSave := authedRequest(handler, cookie, http.MethodPost, "/accounts/bark",
		[]byte(`{"ref":"openid-bark","key":"`+server.URL+`/device-key-5678/"}`))
	if urlSave.Code != http.StatusOK {
		t.Fatalf("save bark url status = %d, body = %s", urlSave.Code, urlSave.Body.String())
	}
	if info := status(); info["key_hint"] != "****5678" {
		t.Fatalf("key_hint after url save = %v, want ****5678", info["key_hint"])
	}
}

func TestParseBarkInput(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		server  string
		want    string
		wantErr bool
	}{
		{"plain key", " jfybct2AGHH8mej2dDeUEJ ", "https://api.day.app", "jfybct2AGHH8mej2dDeUEJ", false},
		{"empty", "", "https://api.day.app", "", false},
		{"official url", "https://api.day.app/jfybct2AGHH8mej2dDeUEJ/", "https://api.day.app", "jfybct2AGHH8mej2dDeUEJ", false},
		{"url without slash", "https://api.day.app/jfybct2AGHH8mej2dDeUEJ", "https://api.day.app", "jfybct2AGHH8mej2dDeUEJ", false},
		{"sub path server", "https://example.com/bark/KEY123/", "https://example.com/bark", "KEY123", false},
		{"host mismatch", "https://other.example.com/KEY/", "https://api.day.app", "", true},
		{"missing key", "https://api.day.app/", "https://api.day.app", "", true},
		{"whitespace key", "abc def", "https://api.day.app", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseBarkInput(tt.input, tt.server)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseBarkInput() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseBarkInput() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("parseBarkInput() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBarkTestUnknownDeviceFriendlyError(t *testing.T) {
	app, handler := newTestApp(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":400,"message":"failed to get device token: failed to get [deadbeef] device token from database"}`))
	}))
	t.Cleanup(server.Close)
	bark, err := notify.NewBark(server.URL, time.Second)
	if err != nil {
		t.Fatalf("NewBark() error = %v", err)
	}
	app.notifier = bark
	if _, err := app.db.UpsertAccount(context.Background(), "openid-bark-error", "buffer", nil, nil, nil, nil, nil, nil); err != nil {
		t.Fatalf("create account: %v", err)
	}
	cookie := loginForTest(t, handler, testAdminUser, testAdminPass)

	rec := authedRequest(handler, cookie, http.MethodPost, "/accounts/bark/test",
		[]byte(`{"ref":"openid-bark-error","key":"deadbeef"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST /accounts/bark/test status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "不存在该 device key") {
		t.Fatalf("friendly error missing, body = %s", rec.Body.String())
	}
}

func TestNotifyAccountExpiredDedup(t *testing.T) {
	app, _ := newTestApp(t)
	server, received := newBarkTestServer(t)
	bark, err := notify.NewBark(server.URL, time.Second)
	if err != nil {
		t.Fatalf("NewBark() error = %v", err)
	}
	app.notifier = bark
	ctx := context.Background()

	acc, err := app.db.UpsertAccount(ctx, "openid-notify", "buffer", nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	if err := app.db.SetAccountBark(ctx, acc.ID, "device-key"); err != nil {
		t.Fatalf("SetAccountBark() error = %v", err)
	}
	acc, err = app.db.GetAccount(ctx, acc.ID)
	if err != nil {
		t.Fatalf("GetAccount() error = %v", err)
	}
	if err := app.db.SetAccountStatus(ctx, acc.ID, "expired"); err != nil {
		t.Fatalf("SetAccountStatus() error = %v", err)
	}

	waitNotification := func() map[string]any {
		t.Helper()
		select {
		case payload := <-received:
			return payload
		case <-time.After(2 * time.Second):
			t.Fatalf("bark notification not received")
			return nil
		}
	}

	app.notifyAccountExpired(acc)
	payload := waitNotification()
	if payload["title"] != "YYB Go 账号失效" {
		t.Fatalf("expired title = %v", payload["title"])
	}
	if body, _ := payload["body"].(string); !strings.Contains(body, "登录已失效") {
		t.Fatalf("expired body = %v", payload["body"])
	}

	// 同一轮失效只通知一次。
	app.notifyAccountExpired(acc)
	select {
	case <-received:
		t.Fatalf("duplicate bark notification sent")
	case <-time.After(200 * time.Millisecond):
	}

	// 恢复可用后再次失效应重新通知。
	if err := app.db.SetAccountStatus(ctx, acc.ID, "alive"); err != nil {
		t.Fatalf("set alive: %v", err)
	}
	if err := app.db.SetAccountStatus(ctx, acc.ID, "expired"); err != nil {
		t.Fatalf("set expired: %v", err)
	}
	app.notifyAccountExpired(acc)
	waitNotification()
}

func TestStoreFromScanSendsRecoveryNotification(t *testing.T) {
	app, _ := newTestApp(t)
	server, received := newBarkTestServer(t)
	bark, err := notify.NewBark(server.URL, time.Second)
	if err != nil {
		t.Fatalf("NewBark() error = %v", err)
	}
	app.notifier = bark
	ctx := context.Background()

	acc, err := app.db.UpsertAccount(ctx, "openid-scan-recover", "old-buffer", nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	if err := app.db.SetAccountBark(ctx, acc.ID, "device-key"); err != nil {
		t.Fatalf("SetAccountBark() error = %v", err)
	}
	if err := app.db.SetAccountStatus(ctx, acc.ID, "expired"); err != nil {
		t.Fatalf("SetAccountStatus() error = %v", err)
	}

	_, err = app.storeFromScan(ctx, "new-buffer", protocol.LoginBufferCredentials{
		OpenID:   "openid-scan-recover",
		Nickname: "scan-user",
	}, nil)
	if err != nil {
		t.Fatalf("storeFromScan() error = %v", err)
	}

	select {
	case payload := <-received:
		if payload["title"] != "YYB Go 账号恢复" {
			t.Fatalf("recovered title = %v", payload["title"])
		}
		if body, _ := payload["body"].(string); !strings.Contains(body, "scan-user") || !strings.Contains(body, "已重新可用") {
			t.Fatalf("recovered body = %v", payload["body"])
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("bark recovery notification not received")
	}
}

func TestNotifyAccountRecovered(t *testing.T) {
	app, _ := newTestApp(t)
	server, received := newBarkTestServer(t)
	bark, err := notify.NewBark(server.URL, time.Second)
	if err != nil {
		t.Fatalf("NewBark() error = %v", err)
	}
	app.notifier = bark
	ctx := context.Background()

	acc, err := app.db.UpsertAccount(ctx, "openid-recovered", "buffer", nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	if err := app.db.SetAccountBark(ctx, acc.ID, "device-key"); err != nil {
		t.Fatalf("SetAccountBark() error = %v", err)
	}
	acc, err = app.db.GetAccount(ctx, acc.ID)
	if err != nil {
		t.Fatalf("GetAccount() error = %v", err)
	}
	if err := app.db.SetAccountStatus(ctx, acc.ID, "expired"); err != nil {
		t.Fatalf("SetAccountStatus() error = %v", err)
	}
	recovered, err := app.db.SetAccountAlive(ctx, acc.ID)
	if err != nil || !recovered {
		t.Fatalf("SetAccountAlive() = %v, %v; want true, nil", recovered, err)
	}

	app.notifyAccountRecovered(acc)
	select {
	case payload := <-received:
		if payload["title"] != "YYB Go 账号恢复" {
			t.Fatalf("recovered title = %v", payload["title"])
		}
		if body, _ := payload["body"].(string); !strings.Contains(body, "已重新可用") {
			t.Fatalf("recovered body = %v", payload["body"])
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("bark recovery notification not received")
	}
}
