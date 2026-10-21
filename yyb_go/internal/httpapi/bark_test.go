package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"yyb_go/internal/notify"
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

	waitNotification := func() {
		t.Helper()
		select {
		case <-received:
		case <-time.After(2 * time.Second):
			t.Fatalf("bark notification not received")
		}
	}

	app.notifyAccountExpired(acc)
	waitNotification()

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
