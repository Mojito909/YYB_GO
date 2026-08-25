package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenMigratesWMPFSessionsTableToSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "yyb.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	ctx := context.Background()
	if _, err = raw.ExecContext(ctx, `
CREATE TABLE wechat_accounts (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    openid          TEXT    NOT NULL UNIQUE,
    uin             INTEGER,
    alias           TEXT,
    nickname        TEXT,
    avatar          TEXT,
    user_info       TEXT,
    login_buffer    TEXT    NOT NULL,
    credentials     TEXT,
    status          TEXT,
    last_checked_at INTEGER,
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
);
CREATE TABLE wmpf_sessions (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    wechat_account_id INTEGER NOT NULL REFERENCES wechat_accounts(id) ON DELETE CASCADE,
    uin               INTEGER,
    tcp_proxy         TEXT    NOT NULL DEFAULT '',
    session_blob      TEXT    NOT NULL,
    expires_at        INTEGER NOT NULL,
    created_at        INTEGER NOT NULL,
    updated_at        INTEGER NOT NULL,
    UNIQUE(wechat_account_id, tcp_proxy)
);
CREATE INDEX idx_sess_expires ON wmpf_sessions(expires_at);
INSERT INTO wechat_accounts(id, openid, login_buffer, created_at, updated_at)
VALUES(1, 'openid-1', 'login-buffer', 10, 10);
INSERT INTO wmpf_sessions(id, wechat_account_id, uin, tcp_proxy, session_blob, expires_at, created_at, updated_at)
VALUES(7, 1, 12345, '', '{"ready":true}', ?, 20, 20);
`, time.Now().Add(time.Hour).Unix()); err != nil {
		_ = raw.Close()
		t.Fatalf("seed old schema: %v", err)
	}
	if err = raw.Close(); err != nil {
		t.Fatalf("close seed db: %v", err)
	}

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()

	oldExists, err := sqliteTableExists(ctx, db.sql, "wmpf_sessions")
	if err != nil {
		t.Fatalf("check old table: %v", err)
	}
	if oldExists {
		t.Fatalf("old wmpf_sessions table still exists")
	}
	newExists, err := sqliteTableExists(ctx, db.sql, "sessions")
	if err != nil {
		t.Fatalf("check new table: %v", err)
	}
	if !newExists {
		t.Fatalf("new sessions table does not exist")
	}

	session, err := db.GetSession(ctx, 1, "")
	if err != nil {
		t.Fatalf("GetSession() error = %v", err)
	}
	if session.ID != 7 {
		t.Fatalf("session id = %d, want 7", session.ID)
	}
	if ready, ok := session.SessionBlob["ready"].(bool); !ok || !ready {
		t.Fatalf("session blob = %#v", session.SessionBlob)
	}
}

func TestGetAPIMetricsBucketsCallsAndLatency(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	now := time.Now().Truncate(time.Second)
	for _, item := range []APICallLog{
		{Endpoint: "/wxapp/getCode", Method: "POST", ClientIP: "10.0.0.1", AppID: "wx-a", StatusCode: 200, Success: true, DurationMS: 100, CreatedAt: now.Add(-2 * time.Hour).Unix()},
		{Endpoint: "/wxapp/getCode", Method: "POST", ClientIP: "10.0.0.2", AppID: "wx-b", StatusCode: 502, Success: false, DurationMS: 900, CreatedAt: now.Add(-time.Hour).Unix()},
	} {
		if err := db.RecordAPICall(ctx, item); err != nil {
			t.Fatalf("RecordAPICall() error = %v", err)
		}
	}
	metrics, err := db.GetAPIMetrics(ctx, "day", now)
	if err != nil {
		t.Fatalf("GetAPIMetrics() error = %v", err)
	}
	if metrics.Calls != 2 || metrics.Success != 1 || metrics.Failed != 1 || metrics.UniqueIPs != 2 {
		t.Fatalf("metrics = %#v", metrics)
	}
	if metrics.AvgDurationMS != 500 || metrics.P95DurationMS != 900 {
		t.Fatalf("latency avg=%v p95=%v", metrics.AvgDurationMS, metrics.P95DurationMS)
	}
	if len(metrics.Buckets) != 24 {
		t.Fatalf("bucket count = %d, want 24", len(metrics.Buckets))
	}
}
