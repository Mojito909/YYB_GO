package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS wechat_accounts (
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
    bark_key        TEXT    NOT NULL DEFAULT '',
    expired_notified INTEGER NOT NULL DEFAULT 0,
    created_at      INTEGER NOT NULL,
    updated_at      INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_wxacc_uin ON wechat_accounts(uin);

CREATE TABLE IF NOT EXISTS sessions (
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
CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);

CREATE TABLE IF NOT EXISTS admin_users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    username      TEXT    NOT NULL UNIQUE,
    password_hash TEXT    NOT NULL,
    created_at    INTEGER NOT NULL,
    updated_at    INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS api_tokens (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    username        TEXT    NOT NULL,
    account_id      INTEGER NOT NULL,
    token_hash      TEXT    NOT NULL UNIQUE,
    created_at      INTEGER NOT NULL,
    last_used_at    INTEGER,
    revoked_at      INTEGER
);
CREATE INDEX IF NOT EXISTS idx_api_tokens_username ON api_tokens(username);
CREATE INDEX IF NOT EXISTS idx_api_tokens_account ON api_tokens(account_id);

CREATE TABLE IF NOT EXISTS api_log_settings (
    id            INTEGER PRIMARY KEY CHECK (id = 1),
    retention_days INTEGER NOT NULL DEFAULT 7 CHECK (retention_days IN (3, 7, 15, 30)),
    updated_at    INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS api_call_logs (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id     INTEGER REFERENCES wechat_accounts(id) ON DELETE SET NULL,
    account_ref    TEXT NOT NULL DEFAULT '',
    endpoint       TEXT NOT NULL,
    method         TEXT NOT NULL,
    app_id         TEXT NOT NULL DEFAULT '',
    client_ip      TEXT NOT NULL DEFAULT '',
    program        TEXT NOT NULL DEFAULT '',
    status_code    INTEGER NOT NULL DEFAULT 200,
    success        INTEGER NOT NULL DEFAULT 1,
    duration_ms    INTEGER NOT NULL DEFAULT 0,
    error_message  TEXT NOT NULL DEFAULT '',
    created_at     INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_api_logs_created ON api_call_logs(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_api_logs_ip ON api_call_logs(client_ip);
CREATE INDEX IF NOT EXISTS idx_api_logs_endpoint ON api_call_logs(endpoint);
`

type DB struct {
	sql *sql.DB
}

type WechatAccount struct {
	ID              int64          `json:"id"`
	OpenID          string         `json:"openid"`
	UIN             *int64         `json:"uin,omitempty"`
	Alias           *string        `json:"alias,omitempty"`
	Nickname        *string        `json:"nickname,omitempty"`
	Avatar          *string        `json:"avatar,omitempty"`
	UserInfo        map[string]any `json:"user_info,omitempty"`
	LoginBuffer     string         `json:"login_buffer,omitempty"`
	Credentials     map[string]any `json:"credentials,omitempty"`
	Status          *string        `json:"status,omitempty"`
	LastCheckedAt   *int64         `json:"last_checked_at,omitempty"`
	BarkKey         string         `json:"-"`
	ExpiredNotified bool           `json:"-"`
	CreatedAt       int64          `json:"created_at"`
	UpdatedAt       int64          `json:"updated_at"`
}

type AccountPublic struct {
	ID            int64   `json:"id"`
	OpenID        string  `json:"openid"`
	UIN           *int64  `json:"uin"`
	Alias         *string `json:"alias"`
	Nickname      *string `json:"nickname"`
	Avatar        *string `json:"avatar"`
	Status        *string `json:"status"`
	LastCheckedAt *int64  `json:"last_checked_at"`
	BarkEnabled   bool    `json:"bark_enabled"`
	CreatedAt     int64   `json:"created_at"`
	UpdatedAt     int64   `json:"updated_at"`
}

type SessionRow struct {
	ID              int64
	WechatAccountID int64
	UIN             *int64
	TCPProxy        string
	SessionBlob     map[string]any
	ExpiresAt       int64
	CreatedAt       int64
	UpdatedAt       int64
}

type AdminUser struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	PasswordHash string `json:"-"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
}

type APITokenMeta struct {
	AccountID  int64
	CreatedAt  int64
	LastUsedAt *int64
}

type APILogSettings struct {
	RetentionDays int   `json:"retention_days"`
	UpdatedAt     int64 `json:"updated_at"`
}

type APICallLog struct {
	ID           int64  `json:"id"`
	AccountID    *int64 `json:"account_id,omitempty"`
	AccountRef   string `json:"account_ref"`
	AccountName  string `json:"account_name,omitempty"`
	Endpoint     string `json:"endpoint"`
	Method       string `json:"method"`
	ClientIP     string `json:"client_ip"`
	AppID        string `json:"app_id"`
	StatusCode   int    `json:"status_code"`
	Success      bool   `json:"success"`
	DurationMS   int64  `json:"duration_ms"`
	ErrorMessage string `json:"error_message,omitempty"`
	CreatedAt    int64  `json:"created_at"`
}

type APICallLogFilter struct {
	Keyword string
	IP      string
	Path    string
	Status  string
	Since   int64
	Until   int64
	Limit   int
	Offset  int
}

type APIMetricBucket struct {
	Label   string `json:"label"`
	Calls   int64  `json:"calls"`
	Success int64  `json:"success"`
	Failed  int64  `json:"failed"`
}

type APIMetrics struct {
	Period        string            `json:"period"`
	Calls         int64             `json:"calls"`
	Success       int64             `json:"success"`
	Failed        int64             `json:"failed"`
	SuccessRate   float64           `json:"success_rate"`
	UniqueIPs     int64             `json:"unique_ips"`
	AvgDurationMS float64           `json:"avg_duration_ms"`
	P95DurationMS int64             `json:"p95_duration_ms"`
	Buckets       []APIMetricBucket `json:"buckets"`
}

// sqliteDSN 把连接级 PRAGMA 放进 DSN，确保每个新建连接都应用相同设置，
// 而不是只在启动时的第一个连接上生效。
func sqliteDSN(path string) string {
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)"
	if path != ":memory:" {
		dsn += "&_pragma=journal_mode(WAL)"
	}
	return dsn
}

func Open(path string) (*DB, error) {
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, err
		}
	}
	db, err := sql.Open("sqlite", sqliteDSN(path))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err = db.ExecContext(ctx, "SELECT 1"); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err = migrateSessionsTable(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err = migrateAPITokensTable(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err = migrateAPICallLogsTable(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err = migrateAccountsBarkColumns(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if _, err = db.ExecContext(ctx, schema); err != nil {
		_ = db.Close()
		return nil, err
	}
	out := &DB{sql: db}
	if err = out.ensureAPILogSettings(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	_, _ = out.PruneAPICallLogs(ctx)
	_, _ = out.PurgeExpiredSessions(ctx)
	return out, nil
}

// migrateAccountsBarkColumns 为旧库补充 Bark 通知相关列。
func migrateAccountsBarkColumns(ctx context.Context, db *sql.DB) error {
	exists, err := sqliteTableExists(ctx, db, "wechat_accounts")
	if err != nil || !exists {
		return err
	}
	columns := []struct {
		name string
		ddl  string
	}{
		{"bark_key", "ALTER TABLE wechat_accounts ADD COLUMN bark_key TEXT NOT NULL DEFAULT ''"},
		{"expired_notified", "ALTER TABLE wechat_accounts ADD COLUMN expired_notified INTEGER NOT NULL DEFAULT 0"},
	}
	for _, col := range columns {
		has, err := sqliteColumnExists(ctx, db, "wechat_accounts", col.name)
		if err != nil {
			return err
		}
		if has {
			continue
		}
		if _, err = db.ExecContext(ctx, col.ddl); err != nil {
			return err
		}
	}
	return nil
}

func migrateAPICallLogsTable(ctx context.Context, db *sql.DB) error {
	exists, err := sqliteTableExists(ctx, db, "api_call_logs")
	if err != nil || !exists {
		return err
	}
	hasAppID, err := sqliteColumnExists(ctx, db, "api_call_logs", "app_id")
	if err != nil || hasAppID {
		return err
	}
	_, err = db.ExecContext(ctx, "ALTER TABLE api_call_logs ADD COLUMN app_id TEXT NOT NULL DEFAULT ''")
	return err
}

func (db *DB) Close() error {
	if db == nil || db.sql == nil {
		return nil
	}
	return db.sql.Close()
}

// Ping 检查数据库连接是否可用。
func (db *DB) Ping(ctx context.Context) error {
	if db == nil || db.sql == nil {
		return errors.New("database is closed")
	}
	return db.sql.PingContext(ctx)
}

func migrateSessionsTable(ctx context.Context, db *sql.DB) error {
	oldExists, err := sqliteTableExists(ctx, db, "wmpf_sessions")
	if err != nil {
		return err
	}
	if !oldExists {
		return nil
	}
	newExists, err := sqliteTableExists(ctx, db, "sessions")
	if err != nil {
		return err
	}
	if !newExists {
		if _, err = db.ExecContext(ctx, "DROP INDEX IF EXISTS idx_sess_expires"); err != nil {
			return err
		}
		_, err = db.ExecContext(ctx, "ALTER TABLE wmpf_sessions RENAME TO sessions")
		return err
	}
	if _, err = db.ExecContext(ctx, `
INSERT OR IGNORE INTO sessions
(id, wechat_account_id, uin, tcp_proxy, session_blob, expires_at, created_at, updated_at)
SELECT id, wechat_account_id, uin, tcp_proxy, session_blob, expires_at, created_at, updated_at
FROM wmpf_sessions`); err != nil {
		return err
	}
	_, err = db.ExecContext(ctx, "DROP TABLE wmpf_sessions")
	return err
}

func sqliteTableExists(ctx context.Context, db *sql.DB, name string) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?", name).Scan(&n)
	return n > 0, err
}

func sqliteColumnExists(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	rows, err := db.QueryContext(ctx, "SELECT name FROM pragma_table_info(?)", table)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false, err
		}
		if name == column {
			return true, rows.Err()
		}
	}
	return false, rows.Err()
}

// migrateAPITokensTable 把旧的管理员级全局令牌表升级为账号级令牌表。
// 旧表没有 account_id，历史令牌无法确定归属，直接清空以避免越权。
func migrateAPITokensTable(ctx context.Context, db *sql.DB) error {
	exists, err := sqliteTableExists(ctx, db, "api_tokens")
	if err != nil || !exists {
		return err
	}
	hasAccount, err := sqliteColumnExists(ctx, db, "api_tokens", "account_id")
	if err != nil || hasAccount {
		return err
	}
	if _, err = db.ExecContext(ctx, "DROP TABLE api_tokens"); err != nil {
		return err
	}
	return nil
}

func (db *DB) UpsertAccount(ctx context.Context, openid, loginBuffer string, alias, nickname, avatar *string, userInfo map[string]any, credentials map[string]any, status *string) (*WechatAccount, error) {
	now := time.Now().Unix()
	userJSON, err := marshalNullable(userInfo)
	if err != nil {
		return nil, err
	}
	credJSON, err := marshalNullable(credentials)
	if err != nil {
		return nil, err
	}
	_, err = db.sql.ExecContext(ctx,
		`INSERT INTO wechat_accounts
		(openid, login_buffer, alias, nickname, avatar, user_info, credentials, status, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(openid) DO UPDATE SET
		login_buffer=excluded.login_buffer, alias=excluded.alias, nickname=excluded.nickname,
		avatar=excluded.avatar, user_info=excluded.user_info, credentials=excluded.credentials,
		status=excluded.status, expired_notified=0, updated_at=excluded.updated_at`,
		openid, loginBuffer, nullableString(alias), nullableString(nickname), nullableString(avatar),
		userJSON, credJSON, nullableString(status), now, now,
	)
	if err != nil {
		return nil, err
	}
	return db.GetAccountByOpenID(ctx, openid)
}

func (db *DB) GetAccount(ctx context.Context, id int64) (*WechatAccount, error) {
	return db.scanAccount(db.sql.QueryRowContext(ctx, selectAccountSQL+" WHERE id=?", id))
}

func (db *DB) GetAccountByOpenID(ctx context.Context, openid string) (*WechatAccount, error) {
	return db.scanAccount(db.sql.QueryRowContext(ctx, selectAccountSQL+" WHERE openid=?", openid))
}

func (db *DB) GetAccountByUIN(ctx context.Context, uin int64) (*WechatAccount, error) {
	return db.scanAccount(db.sql.QueryRowContext(ctx, selectAccountSQL+" WHERE uin=?", uin))
}

// ErrAmbiguousRef 表示纯数字 ref 同时匹配到了不同的账号 id 和 uin。
var ErrAmbiguousRef = errors.New("ambiguous account ref")

// ResolveAccount 支持 openid、纯数字（uin 或 id）以及 id: / uin: / openid: 前缀。
// 纯数字同时命中两个不同账号时返回 ErrAmbiguousRef，提示调用方使用前缀。
func (db *DB) ResolveAccount(ctx context.Context, ref string) (*WechatAccount, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, sql.ErrNoRows
	}
	switch {
	case strings.HasPrefix(ref, "id:"):
		n, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(ref, "id:")), 10, 64)
		if err != nil {
			return nil, sql.ErrNoRows
		}
		return db.GetAccount(ctx, n)
	case strings.HasPrefix(ref, "uin:"):
		n, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(ref, "uin:")), 10, 64)
		if err != nil {
			return nil, sql.ErrNoRows
		}
		return db.GetAccountByUIN(ctx, n)
	case strings.HasPrefix(ref, "openid:"):
		return db.GetAccountByOpenID(ctx, strings.TrimSpace(strings.TrimPrefix(ref, "openid:")))
	}
	if isDigits(ref) {
		n, _ := strconv.ParseInt(ref, 10, 64)
		byUIN, uinErr := db.GetAccountByUIN(ctx, n)
		if uinErr != nil && !errors.Is(uinErr, sql.ErrNoRows) {
			return nil, uinErr
		}
		byID, idErr := db.GetAccount(ctx, n)
		if idErr != nil && !errors.Is(idErr, sql.ErrNoRows) {
			return nil, idErr
		}
		if uinErr == nil && idErr == nil && byUIN.ID != byID.ID {
			return nil, fmt.Errorf("%w: %s", ErrAmbiguousRef, ref)
		}
		if uinErr == nil {
			return byUIN, nil
		}
		return byID, idErr
	}
	return db.GetAccountByOpenID(ctx, ref)
}

func (db *DB) ListAccounts(ctx context.Context) ([]*WechatAccount, error) {
	rows, err := db.sql.QueryContext(ctx, selectAccountSQL+" ORDER BY id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*WechatAccount
	for rows.Next() {
		acc, err := scanAccountRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, acc)
	}
	return out, rows.Err()
}

func (db *DB) SetAccountUIN(ctx context.Context, id, uin int64) error {
	_, err := db.sql.ExecContext(ctx, "UPDATE wechat_accounts SET uin=?, updated_at=? WHERE id=?", uin, time.Now().Unix(), id)
	return err
}

func (db *DB) SetAccountProfile(ctx context.Context, id int64, nickname, avatar *string, userInfo map[string]any) error {
	userJSON, err := marshalNullable(userInfo)
	if err != nil {
		return err
	}
	_, err = db.sql.ExecContext(ctx,
		"UPDATE wechat_accounts SET nickname=?, avatar=?, user_info=?, updated_at=? WHERE id=?",
		nullableString(nickname), nullableString(avatar), userJSON, time.Now().Unix(), id,
	)
	return err
}

func (db *DB) SetAccountCredential(ctx context.Context, id int64, loginBuffer string, credentials map[string]any) error {
	credJSON, err := marshalNullable(credentials)
	if err != nil {
		return err
	}
	_, err = db.sql.ExecContext(ctx,
		"UPDATE wechat_accounts SET login_buffer=?, credentials=?, updated_at=? WHERE id=?",
		loginBuffer, credJSON, time.Now().Unix(), id,
	)
	return err
}

func (db *DB) SetAccountStatus(ctx context.Context, id int64, status string) error {
	now := time.Now().Unix()
	// 账号恢复可用后重置通知标记，下一次失效可以再次推送。
	if status == "alive" {
		_, err := db.sql.ExecContext(ctx,
			"UPDATE wechat_accounts SET status=?, last_checked_at=?, updated_at=?, expired_notified=0 WHERE id=?",
			status, now, now, id,
		)
		return err
	}
	_, err := db.sql.ExecContext(ctx,
		"UPDATE wechat_accounts SET status=?, last_checked_at=?, updated_at=? WHERE id=?",
		status, now, now, id,
	)
	return err
}

// SetAccountBark 保存账号的 Bark device key，空字符串表示关闭通知。
func (db *DB) SetAccountBark(ctx context.Context, id int64, key string) error {
	_, err := db.sql.ExecContext(ctx,
		"UPDATE wechat_accounts SET bark_key=?, updated_at=? WHERE id=?",
		key, time.Now().Unix(), id,
	)
	return err
}

// ClaimAccountExpiredNotification 原子地领取一次失效通知。
// 返回 true 表示本次调用成功抢占，应发送推送；重复调用返回 false。
func (db *DB) ClaimAccountExpiredNotification(ctx context.Context, id int64) (bool, error) {
	res, err := db.sql.ExecContext(ctx,
		"UPDATE wechat_accounts SET expired_notified=1 WHERE id=? AND expired_notified=0", id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (db *DB) DeleteAccount(ctx context.Context, id int64) error {
	if _, err := db.sql.ExecContext(ctx, "DELETE FROM api_tokens WHERE account_id=?", id); err != nil {
		return err
	}
	_, err := db.sql.ExecContext(ctx, "DELETE FROM wechat_accounts WHERE id=?", id)
	return err
}

func (db *DB) GetSession(ctx context.Context, accountID int64, tcpProxy string) (*SessionRow, error) {
	row := db.sql.QueryRowContext(ctx,
		"SELECT id, wechat_account_id, uin, tcp_proxy, session_blob, expires_at, created_at, updated_at FROM sessions WHERE wechat_account_id=? AND tcp_proxy=? AND expires_at>?",
		accountID, tcpProxy, time.Now().Unix(),
	)
	return scanSession(row)
}

func (db *DB) PutSession(ctx context.Context, accountID int64, uin *int64, sessionBlob map[string]any, expiresAt int64, tcpProxy string) error {
	now := time.Now().Unix()
	blob, err := json.Marshal(sessionBlob)
	if err != nil {
		return err
	}
	_, err = db.sql.ExecContext(ctx,
		`INSERT INTO sessions
		(wechat_account_id, uin, tcp_proxy, session_blob, expires_at, created_at, updated_at)
		VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(wechat_account_id, tcp_proxy) DO UPDATE SET
		uin=excluded.uin, session_blob=excluded.session_blob,
		expires_at=excluded.expires_at, updated_at=excluded.updated_at`,
		accountID, nullableInt(uin), tcpProxy, string(blob), expiresAt, now, now,
	)
	return err
}

func (db *DB) InvalidateSession(ctx context.Context, accountID int64, tcpProxy string) error {
	_, err := db.sql.ExecContext(ctx, "DELETE FROM sessions WHERE wechat_account_id=? AND tcp_proxy=?", accountID, tcpProxy)
	return err
}

func (db *DB) PurgeExpiredSessions(ctx context.Context) (int64, error) {
	res, err := db.sql.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at<=?", time.Now().Unix())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// CountAdminUsers 返回管理员用户数量，用于判断是否需要初始化引导账号。
func (db *DB) CountAdminUsers(ctx context.Context) (int, error) {
	var n int
	err := db.sql.QueryRowContext(ctx, "SELECT count(*) FROM admin_users").Scan(&n)
	return n, err
}

// EnsureAdminUser 在用户名不存在时创建管理员，已存在则忽略。
func (db *DB) EnsureAdminUser(ctx context.Context, username, passwordHash string) error {
	now := time.Now().Unix()
	_, err := db.sql.ExecContext(ctx,
		"INSERT OR IGNORE INTO admin_users(username, password_hash, created_at, updated_at) VALUES(?,?,?,?)",
		username, passwordHash, now, now,
	)
	return err
}

// GetAdminUser 按用户名查询管理员。
func (db *DB) GetAdminUser(ctx context.Context, username string) (*AdminUser, error) {
	var u AdminUser
	err := db.sql.QueryRowContext(ctx,
		"SELECT id, username, password_hash, created_at, updated_at FROM admin_users WHERE username=?",
		username,
	).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// SetAdminPassword 更新管理员密码哈希。
func (db *DB) SetAdminPassword(ctx context.Context, username, passwordHash string) error {
	_, err := db.sql.ExecContext(ctx,
		"UPDATE admin_users SET password_hash=?, updated_at=? WHERE username=?",
		passwordHash, time.Now().Unix(), username,
	)
	return err
}

// RotateAPIToken 为 (username, accountID) 生成新令牌，并吊销该组合下的旧令牌。
// 其他账号的令牌不受影响。
func (db *DB) RotateAPIToken(ctx context.Context, username string, accountID int64, tokenHash string) error {
	now := time.Now().Unix()
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx,
		"UPDATE api_tokens SET revoked_at=? WHERE username=? AND account_id=? AND revoked_at IS NULL",
		now, username, accountID,
	); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx,
		"INSERT INTO api_tokens(username, account_id, token_hash, created_at) VALUES(?,?,?,?)",
		username, accountID, tokenHash, now,
	); err != nil {
		return err
	}
	return tx.Commit()
}

// apiTokenTouchInterval 限制 last_used_at 的写入频率，避免每个请求都产生一次写事务。
const apiTokenTouchInterval = 60

// ValidateAPIToken 校验令牌哈希，返回所属管理员用户名与绑定的账号 ID。
func (db *DB) ValidateAPIToken(ctx context.Context, tokenHash string) (string, int64, bool, error) {
	var username string
	var id, accountID int64
	var lastUsed sql.NullInt64
	err := db.sql.QueryRowContext(ctx,
		"SELECT id, username, account_id, last_used_at FROM api_tokens WHERE token_hash=? AND revoked_at IS NULL",
		tokenHash,
	).Scan(&id, &username, &accountID, &lastUsed)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, false, nil
	}
	if err != nil {
		return "", 0, false, err
	}
	now := time.Now().Unix()
	if !lastUsed.Valid || now-lastUsed.Int64 >= apiTokenTouchInterval {
		// 最近使用时间是辅助元数据，写入失败不影响本次鉴权结果。
		_, _ = db.sql.ExecContext(ctx,
			"UPDATE api_tokens SET last_used_at=? WHERE id=?",
			now, id,
		)
	}
	return username, accountID, true, nil
}

// RevokeAllAPITokens 吊销指定管理员在所有账号上的有效令牌（改密时使用）。
func (db *DB) RevokeAllAPITokens(ctx context.Context, username string) error {
	_, err := db.sql.ExecContext(ctx,
		"UPDATE api_tokens SET revoked_at=? WHERE username=? AND revoked_at IS NULL",
		time.Now().Unix(), username,
	)
	return err
}

// RevokeAPITokens 吊销指定管理员在指定账号上的全部有效令牌。
func (db *DB) RevokeAPITokens(ctx context.Context, username string, accountID int64) error {
	_, err := db.sql.ExecContext(ctx,
		"UPDATE api_tokens SET revoked_at=? WHERE username=? AND account_id=? AND revoked_at IS NULL",
		time.Now().Unix(), username, accountID,
	)
	return err
}

// ActiveAPITokenMeta 查询指定管理员在指定账号上的当前有效令牌元信息。
func (db *DB) ActiveAPITokenMeta(ctx context.Context, username string, accountID int64) (*APITokenMeta, error) {
	var meta APITokenMeta
	var lastUsed sql.NullInt64
	err := db.sql.QueryRowContext(ctx,
		"SELECT created_at, last_used_at, account_id FROM api_tokens WHERE username=? AND account_id=? AND revoked_at IS NULL ORDER BY id DESC LIMIT 1",
		username, accountID,
	).Scan(&meta.CreatedAt, &lastUsed, &meta.AccountID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if lastUsed.Valid {
		meta.LastUsedAt = &lastUsed.Int64
	}
	return &meta, nil
}

func (db *DB) ensureAPILogSettings(ctx context.Context) error {
	_, err := db.sql.ExecContext(ctx,
		"INSERT OR IGNORE INTO api_log_settings(id, retention_days, updated_at) VALUES(1, 7, ?)", time.Now().Unix())
	return err
}

func (db *DB) GetAPILogSettings(ctx context.Context) (APILogSettings, error) {
	var out APILogSettings
	err := db.sql.QueryRowContext(ctx, "SELECT retention_days, updated_at FROM api_log_settings WHERE id=1").Scan(&out.RetentionDays, &out.UpdatedAt)
	return out, err
}

func (db *DB) SetAPILogRetention(ctx context.Context, days int) (APILogSettings, error) {
	if days != 3 && days != 7 && days != 15 && days != 30 {
		return APILogSettings{}, fmt.Errorf("retention_days must be one of 3, 7, 15, 30")
	}
	now := time.Now().Unix()
	if _, err := db.sql.ExecContext(ctx, "UPDATE api_log_settings SET retention_days=?, updated_at=? WHERE id=1", days, now); err != nil {
		return APILogSettings{}, err
	}
	if _, err := db.PruneAPICallLogs(ctx); err != nil {
		return APILogSettings{}, err
	}
	return APILogSettings{RetentionDays: days, UpdatedAt: now}, nil
}

func (db *DB) RecordAPICall(ctx context.Context, log APICallLog) error {
	return db.InsertAPICallLogs(ctx, []APICallLog{log})
}

// InsertAPICallLogs 在一个事务内批量写入接口日志。
// 过期清理由调用方的定时任务负责，避免每次调用都触发 DELETE。
func (db *DB) InsertAPICallLogs(ctx context.Context, logs []APICallLog) error {
	if len(logs) == 0 {
		return nil
	}
	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO api_call_logs
		(account_id, account_ref, endpoint, method, app_id, client_ip, program, status_code, success, duration_ms, error_message, created_at)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, log := range logs {
		if _, err = stmt.ExecContext(ctx,
			log.AccountID, log.AccountRef, log.Endpoint, log.Method, log.AppID, log.ClientIP, "",
			log.StatusCode, boolInt(log.Success), log.DurationMS, log.ErrorMessage, log.CreatedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (db *DB) PruneAPICallLogs(ctx context.Context) (int64, error) {
	var days int
	if err := db.sql.QueryRowContext(ctx, "SELECT retention_days FROM api_log_settings WHERE id=1").Scan(&days); err != nil {
		return 0, err
	}
	result, err := db.sql.ExecContext(ctx, "DELETE FROM api_call_logs WHERE created_at < ?", time.Now().Add(-time.Duration(days)*24*time.Hour).Unix())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (db *DB) ListAPICallLogs(ctx context.Context, filter APICallLogFilter) ([]APICallLog, int64, error) {
	limit := filter.Limit
	if limit != 20 && limit != 50 && limit != 100 {
		limit = 20
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	where := []string{"1=1"}
	args := make([]any, 0, 8)
	if filter.Keyword != "" {
		where = append(where, "(l.account_ref LIKE ? OR l.endpoint LIKE ? OR l.app_id LIKE ? OR COALESCE(a.nickname, '') LIKE ? OR COALESCE(a.alias, '') LIKE ?)")
		kw := "%" + filter.Keyword + "%"
		args = append(args, kw, kw, kw, kw, kw)
	}
	if filter.IP != "" {
		where = append(where, "l.client_ip LIKE ?")
		args = append(args, "%"+filter.IP+"%")
	}
	if filter.Path != "" {
		where = append(where, "l.endpoint = ?")
		args = append(args, filter.Path)
	}
	if filter.Status == "success" {
		where = append(where, "l.success=1")
	} else if filter.Status == "error" {
		where = append(where, "l.success=0")
	}
	if filter.Since > 0 {
		where = append(where, "l.created_at >= ?")
		args = append(args, filter.Since)
	}
	if filter.Until > 0 {
		where = append(where, "l.created_at <= ?")
		args = append(args, filter.Until)
	}
	whereSQL := strings.Join(where, " AND ")
	var total int64
	countArgs := append([]any(nil), args...)
	if err := db.sql.QueryRowContext(ctx, "SELECT COUNT(*) FROM api_call_logs l LEFT JOIN wechat_accounts a ON a.id=l.account_id WHERE "+whereSQL, countArgs...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, limit, filter.Offset)
	rows, err := db.sql.QueryContext(ctx, `SELECT l.id, l.account_id, l.account_ref,
		COALESCE(a.nickname, a.alias, ''), l.endpoint, l.method, l.app_id, l.client_ip,
		l.status_code, l.success, l.duration_ms, l.error_message, l.created_at
		FROM api_call_logs l LEFT JOIN wechat_accounts a ON a.id=l.account_id
		WHERE `+whereSQL+` ORDER BY l.created_at DESC, l.id DESC LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]APICallLog, 0)
	for rows.Next() {
		var item APICallLog
		var accountID sql.NullInt64
		var success int
		if err := rows.Scan(&item.ID, &accountID, &item.AccountRef, &item.AccountName, &item.Endpoint, &item.Method, &item.AppID, &item.ClientIP, &item.StatusCode, &success, &item.DurationMS, &item.ErrorMessage, &item.CreatedAt); err != nil {
			return nil, 0, err
		}
		if accountID.Valid {
			item.AccountID = &accountID.Int64
		}
		item.Success = success != 0
		out = append(out, item)
	}
	return out, total, rows.Err()
}

func (db *DB) DeleteAPICallLog(ctx context.Context, id int64) error {
	_, err := db.sql.ExecContext(ctx, "DELETE FROM api_call_logs WHERE id=?", id)
	return err
}

func (db *DB) ClearAPICallLogs(ctx context.Context) (int64, error) {
	result, err := db.sql.ExecContext(ctx, "DELETE FROM api_call_logs")
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (db *DB) GetAPIMetrics(ctx context.Context, period string, now time.Time) (APIMetrics, error) {
	if period != "day" && period != "week" && period != "month" {
		period = "day"
	}
	window := 24 * time.Hour
	bucketCount := 24
	bucketSize := time.Hour
	if period == "week" {
		window, bucketCount, bucketSize = 7*24*time.Hour, 7, 24*time.Hour
	} else if period == "month" {
		window, bucketCount, bucketSize = 30*24*time.Hour, 30, 24*time.Hour
	}
	start := now.Add(-window)
	buckets := make([]APIMetricBucket, bucketCount)
	for i := range buckets {
		at := start.Add(time.Duration(i) * bucketSize)
		if period == "day" {
			buckets[i].Label = at.Format("15:04")
		} else {
			buckets[i].Label = at.Format("01/02")
		}
	}
	metrics := APIMetrics{Period: period, Buckets: buckets}
	startUnix, endUnix := start.Unix(), now.Unix()
	var durationTotal int64
	if err := db.sql.QueryRowContext(ctx, `SELECT COUNT(*),
		COALESCE(SUM(success), 0),
		COUNT(DISTINCT CASE WHEN client_ip <> '' THEN client_ip END),
		COALESCE(SUM(duration_ms), 0)
		FROM api_call_logs WHERE created_at >= ? AND created_at <= ?`,
		startUnix, endUnix).Scan(&metrics.Calls, &metrics.Success, &metrics.UniqueIPs, &durationTotal); err != nil {
		return APIMetrics{}, err
	}
	metrics.Failed = metrics.Calls - metrics.Success

	rows, err := db.sql.QueryContext(ctx, `SELECT CAST((created_at - ?) / ? AS INTEGER) AS bucket,
		COUNT(*), COALESCE(SUM(success), 0)
		FROM api_call_logs WHERE created_at >= ? AND created_at <= ?
		GROUP BY bucket`, startUnix, int64(bucketSize/time.Second), startUnix, endUnix)
	if err != nil {
		return APIMetrics{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var index int
		var calls, success int64
		if err := rows.Scan(&index, &calls, &success); err != nil {
			return APIMetrics{}, err
		}
		if index < 0 {
			index = 0
		}
		if index >= bucketCount {
			index = bucketCount - 1
		}
		buckets[index].Calls = calls
		buckets[index].Success = success
		buckets[index].Failed = calls - success
	}
	if err := rows.Err(); err != nil {
		return APIMetrics{}, err
	}
	if metrics.Calls > 0 {
		metrics.SuccessRate = float64(metrics.Success) / float64(metrics.Calls) * 100
		metrics.AvgDurationMS = float64(durationTotal) / float64(metrics.Calls)
		offset := int(math.Ceil(float64(metrics.Calls)*0.95)) - 1
		if offset < 0 {
			offset = 0
		}
		var p95 int64
		err := db.sql.QueryRowContext(ctx, `SELECT duration_ms FROM api_call_logs
			WHERE created_at >= ? AND created_at <= ?
			ORDER BY duration_ms ASC LIMIT 1 OFFSET ?`,
			startUnix, endUnix, offset).Scan(&p95)
		if err == nil {
			metrics.P95DurationMS = p95
		} else if !errors.Is(err, sql.ErrNoRows) {
			return APIMetrics{}, err
		}
	}
	return metrics, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (a *WechatAccount) Public() AccountPublic {
	return AccountPublic{
		ID:            a.ID,
		OpenID:        a.OpenID,
		UIN:           a.UIN,
		Alias:         a.Alias,
		Nickname:      a.Nickname,
		Avatar:        a.Avatar,
		Status:        a.Status,
		LastCheckedAt: a.LastCheckedAt,
		BarkEnabled:   a.BarkKey != "",
		CreatedAt:     a.CreatedAt,
		UpdatedAt:     a.UpdatedAt,
	}
}

const selectAccountSQL = `SELECT id, openid, uin, alias, nickname, avatar, user_info, login_buffer, credentials, status, last_checked_at, bark_key, expired_notified, created_at, updated_at FROM wechat_accounts`

type accountScanner interface {
	Scan(dest ...any) error
}

func (db *DB) scanAccount(row accountScanner) (*WechatAccount, error) {
	return scanAccountRows(row)
}

func scanAccountRows(row accountScanner) (*WechatAccount, error) {
	var (
		a                       WechatAccount
		uin, lastChecked        sql.NullInt64
		alias, nickname, avatar sql.NullString
		userJSON, credJSON      sql.NullString
		status                  sql.NullString
		expiredNotified         int
	)
	err := row.Scan(
		&a.ID, &a.OpenID, &uin, &alias, &nickname, &avatar, &userJSON,
		&a.LoginBuffer, &credJSON, &status, &lastChecked, &a.BarkKey, &expiredNotified,
		&a.CreatedAt, &a.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	a.ExpiredNotified = expiredNotified != 0
	if uin.Valid {
		a.UIN = &uin.Int64
	}
	a.Alias = stringPtrFromNull(alias)
	a.Nickname = stringPtrFromNull(nickname)
	a.Avatar = stringPtrFromNull(avatar)
	a.Status = stringPtrFromNull(status)
	if lastChecked.Valid {
		a.LastCheckedAt = &lastChecked.Int64
	}
	if userJSON.Valid && userJSON.String != "" {
		_ = json.Unmarshal([]byte(userJSON.String), &a.UserInfo)
	}
	if credJSON.Valid && credJSON.String != "" {
		_ = json.Unmarshal([]byte(credJSON.String), &a.Credentials)
	}
	return &a, nil
}

func scanSession(row accountScanner) (*SessionRow, error) {
	var s SessionRow
	var uin sql.NullInt64
	var blob string
	if err := row.Scan(&s.ID, &s.WechatAccountID, &uin, &s.TCPProxy, &blob, &s.ExpiresAt, &s.CreatedAt, &s.UpdatedAt); err != nil {
		return nil, err
	}
	if uin.Valid {
		s.UIN = &uin.Int64
	}
	if err := json.Unmarshal([]byte(blob), &s.SessionBlob); err != nil {
		return nil, fmt.Errorf("decode session_blob: %w", err)
	}
	return &s, nil
}

func marshalNullable(v map[string]any) (sql.NullString, error) {
	if v == nil {
		return sql.NullString{}, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return sql.NullString{}, err
	}
	return sql.NullString{String: string(b), Valid: true}, nil
}

func nullableString(s *string) sql.NullString {
	if s == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *s, Valid: true}
}

func nullableInt(v *int64) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *v, Valid: true}
}

func stringPtrFromNull(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	return &v.String
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
