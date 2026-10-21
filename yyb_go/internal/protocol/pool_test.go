package protocol

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"yyb_go/internal/store"
)

func TestCachedSessionDropsCorruptBlob(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "pool.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer db.Close()
	ctx := context.Background()
	acc, err := db.UpsertAccount(ctx, "openid-corrupt", "login-buffer", nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	if err := db.PutSession(ctx, acc.ID, nil, map[string]any{"broken": true}, time.Now().Add(time.Hour).Unix(), ""); err != nil {
		t.Fatalf("PutSession() error = %v", err)
	}

	pool := NewPool(DefaultConfig(), db)
	_, ok, err := pool.cachedSession(ctx, acc.ID, "")
	if err != nil {
		t.Fatalf("cachedSession() error = %v", err)
	}
	if ok {
		t.Fatalf("cachedSession() returned ok for corrupt blob")
	}
	if _, err := db.GetSession(ctx, acc.ID, ""); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("corrupt session still present, GetSession() error = %v", err)
	}
}
