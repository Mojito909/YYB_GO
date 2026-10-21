package notify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSendPostsToPushEndpoint(t *testing.T) {
	var gotPath string
	var payload pushPayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_ = json.NewDecoder(r.Body).Decode(&payload)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"code":200,"message":"success"}`)
	}))
	defer server.Close()

	bark, err := NewBark(server.URL+"/", time.Second)
	if err != nil {
		t.Fatalf("NewBark() error = %v", err)
	}
	if err := bark.Send(context.Background(), "device-key", "标题", "正文"); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if gotPath != "/push" {
		t.Fatalf("push path = %q, want /push", gotPath)
	}
	if payload.DeviceKey != "device-key" || payload.Title != "标题" || payload.Body != "正文" || payload.Group != "YYB Go" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestSendReportsUpstreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"code":400,"message":"invalid device key"}`)
	}))
	defer server.Close()

	bark, err := NewBark(server.URL, time.Second)
	if err != nil {
		t.Fatalf("NewBark() error = %v", err)
	}
	err = bark.Send(context.Background(), "bad-key", "标题", "正文")
	if err == nil || !strings.Contains(err.Error(), "invalid device key") {
		t.Fatalf("Send() error = %v, want upstream message", err)
	}
}

func TestSendClassifiesUnregisteredDevice(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"code":400,"message":"failed to get device token: failed to get [abcd] device token from database","timestamp":1}`)
	}))
	defer server.Close()

	bark, err := NewBark(server.URL, time.Second)
	if err != nil {
		t.Fatalf("NewBark() error = %v", err)
	}
	if err := bark.Send(context.Background(), "abcd", "标题", "正文"); !errors.Is(err, ErrDeviceNotRegistered) {
		t.Fatalf("Send() error = %v, want ErrDeviceNotRegistered", err)
	}
}

func TestNewBarkRejectsInvalidServer(t *testing.T) {
	if _, err := NewBark("ftp://example.com", time.Second); err == nil {
		t.Fatalf("NewBark() accepted ftp server")
	}
}
