package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"math/big"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"yyb_go/internal/store"
)

const (
	authCookieName = "yyb_session"
	adminUserEnv   = "YYB_ADMIN_USER"
	adminPassEnv   = "YYB_ADMIN_PASSWORD"
	minPasswordLen = 6
)

// authManager 管理管理员登录会话。会话保存在内存中，服务重启后需重新登录。
type authManager struct {
	db         *store.DB
	ttl        time.Duration
	cookieName string

	mu       sync.Mutex
	sessions map[string]*authSession
}

type authSession struct {
	username  string
	expiresAt time.Time
}

func newAuthManager(db *store.DB, ttl time.Duration) *authManager {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &authManager{
		db:         db,
		ttl:        ttl,
		cookieName: authCookieName,
		sessions:   map[string]*authSession{},
	}
}

// bootstrap 在首次启动（无管理员）时创建初始账号：
// 密码取 YYB_ADMIN_PASSWORD，未设置则生成随机密码并打印到日志。
func (m *authManager) bootstrap(ctx context.Context) error {
	count, err := m.db.CountAdminUsers(ctx)
	if err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	username := strings.TrimSpace(os.Getenv(adminUserEnv))
	if username == "" {
		username = "admin"
	}
	password := os.Getenv(adminPassEnv)
	generated := false
	if password == "" {
		password, err = randomPassword(12)
		if err != nil {
			return err
		}
		generated = true
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	if err := m.db.EnsureAdminUser(ctx, username, string(hash)); err != nil {
		return err
	}
	if generated {
		log.Printf("[auth] 已创建初始管理员 用户名=%s 初始密码=%s （仅显示一次，请登录后尽快修改密码）", username, password)
	} else {
		log.Printf("[auth] 已根据环境变量 %s 创建初始管理员 用户名=%s", adminPassEnv, username)
	}
	return nil
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (m *authManager) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body loginRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	body.Username = strings.TrimSpace(body.Username)
	if body.Username == "" || body.Password == "" {
		writeError(w, http.StatusBadRequest, "username and password are required")
		return
	}
	user, err := m.db.GetAdminUser(r.Context(), body.Username)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// 与密码错误保持一致的响应，避免用户名枚举
			writeError(w, http.StatusUnauthorized, "用户名或密码不正确")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(body.Password)) != nil {
		writeError(w, http.StatusUnauthorized, "用户名或密码不正确")
		return
	}
	token, err := m.createSession(user.Username)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	http.SetCookie(w, m.sessionCookie(r, token, m.ttl))
	writeJSON(w, http.StatusOK, map[string]any{"username": user.Username})
}

func (m *authManager) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if c, err := r.Cookie(m.cookieName); err == nil {
		m.destroySession(c.Value)
	}
	// 过期 cookie 使浏览器立即清除
	http.SetCookie(w, m.sessionCookie(r, "", -time.Second))
	writeJSON(w, http.StatusOK, map[string]any{"logged_out": true})
}

func (m *authManager) handleMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	username, ok := m.currentUsername(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"username": username})
}

type changePasswordRequest struct {
	OldPassword string `json:"old_password"`
	NewPassword string `json:"new_password"`
}

func (m *authManager) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	username, ok := m.currentUsername(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	var body changePasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.OldPassword == "" || body.NewPassword == "" {
		writeError(w, http.StatusBadRequest, "old_password and new_password are required")
		return
	}
	if len(body.NewPassword) < minPasswordLen {
		writeError(w, http.StatusBadRequest, "新密码至少 6 位")
		return
	}
	user, err := m.db.GetAdminUser(r.Context(), username)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(body.OldPassword)) != nil {
		writeError(w, http.StatusUnauthorized, "旧密码不正确")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(body.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := m.db.SetAdminPassword(r.Context(), username, string(hash)); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := m.db.RevokeAllAPITokens(r.Context(), username); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// 改密后销毁其他会话，仅保留当前会话
	m.destroySessionsExceptCurrent(r)
	writeJSON(w, http.StatusOK, map[string]any{"password_changed": true})
}

// handleToken 生成、查看和撤销某个微信账号的 API Bearer Token。
// 令牌与账号一对一绑定，只能操作该账号；明文令牌只在生成时返回，数据库仅保存 SHA-256 哈希。
func (m *authManager) handleToken(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	username, ok := m.currentCookieUsername(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "请先通过管理后台登录")
		return
	}
	switch r.Method {
	case http.MethodPost, http.MethodDelete:
		ref, err := requestRef(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		acc, ok := m.resolveTokenAccount(w, r, ref)
		if !ok {
			return
		}
		if r.Method == http.MethodDelete {
			if err := m.db.RevokeAPITokens(r.Context(), username, acc.ID); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"revoked": true, "account_id": acc.ID})
			return
		}
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		token := "yyb_" + hex.EncodeToString(raw)
		if err := m.db.RotateAPIToken(r.Context(), username, acc.ID, hashAPIToken(token)); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"token":      token,
			"token_type": "Bearer",
			"username":   username,
			"account_id": acc.ID,
			"openid":     acc.OpenID,
			"created_at": time.Now().Unix(),
		})
	case http.MethodGet:
		acc, ok := m.resolveTokenAccount(w, r, strings.TrimSpace(r.URL.Query().Get("ref")))
		if !ok {
			return
		}
		meta, err := m.db.ActiveAPITokenMeta(r.Context(), username, acc.ID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if meta == nil {
			writeJSON(w, http.StatusOK, map[string]any{"active": false, "account_id": acc.ID})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"active":       true,
			"account_id":   acc.ID,
			"created_at":   meta.CreatedAt,
			"last_used_at": meta.LastUsedAt,
		})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// resolveTokenAccount 把 ref 解析为微信账号，令牌必须绑定到一个已存在的账号。
func (m *authManager) resolveTokenAccount(w http.ResponseWriter, r *http.Request, ref string) (*store.WechatAccount, bool) {
	if ref == "" {
		writeError(w, http.StatusBadRequest, "ref is required：令牌必须绑定到具体账号")
		return nil, false
	}
	acc, err := m.db.ResolveAccount(r.Context(), ref)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "account not found: "+ref)
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return nil, false
	}
	return acc, true
}

// principal 表示一次请求的调用者身份。
// accountID 为 0 表示管理后台 Cookie 会话（可访问全部账号）；
// viaToken 为 true 表示账号级 API 令牌，只能访问 accountID 对应的账号。
type principal struct {
	username  string
	accountID int64
	viaToken  bool
}

type principalCtxKey struct{}

func withPrincipal(ctx context.Context, p principal) context.Context {
	return context.WithValue(ctx, principalCtxKey{}, p)
}

func principalFrom(ctx context.Context) (principal, bool) {
	p, ok := ctx.Value(principalCtxKey{}).(principal)
	return p, ok
}

// tokenAllowedPaths 列出账号级令牌可访问的接口，其余（页面、扫码、文档、删除账号）一律 403。
var tokenAllowedPaths = map[string]string{
	"/accounts":             http.MethodGet,
	"/accounts/avatar":      http.MethodGet,
	"/accounts/refresh":     http.MethodPost,
	"/accounts/resync":      http.MethodPost,
	"/wxapp/getCode":        http.MethodPost,
	"/wxapp/getPhoneNumber": http.MethodPost,
	"/wxapp/operateWxData":  http.MethodPost,
}

// ginMiddleware 校验会话：API 请求未认证返回 401，页面请求跳转 /login。
// 账号级 API 令牌还会额外校验请求目标账号，越权返回 403。
func (m *authManager) ginMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		p, ok := m.currentPrincipal(c.Request)
		if !ok {
			if isBrowserPagePath(c.Request.URL.Path) {
				c.Redirect(http.StatusFound, "/login")
				c.Abort()
				return
			}
			c.AbortWithStatusJSON(http.StatusUnauthorized, apiEnvelope{
				Code: http.StatusUnauthorized,
				Msg:  "未登录或会话已过期",
				Data: nil,
			})
			return
		}
		if p.viaToken && !m.authorizeToken(c, p) {
			return
		}
		c.Request = c.Request.WithContext(withPrincipal(c.Request.Context(), p))
		c.Next()
	}
}

// authorizeToken 校验账号级令牌的访问范围：接口白名单 + 目标账号归属。
func (m *authManager) authorizeToken(c *gin.Context, p principal) bool {
	abort := func(code int, msg string) bool {
		c.AbortWithStatusJSON(code, apiEnvelope{Code: code, Msg: msg, Data: nil})
		return false
	}
	method, allowed := tokenAllowedPaths[c.Request.URL.Path]
	if !allowed || c.Request.Method != method {
		return abort(http.StatusForbidden, "API 令牌无权访问该接口")
	}
	// GET /accounts 不带 ref，由处理器按令牌绑定账号收窄返回结果
	if c.Request.URL.Path == "/accounts" {
		return true
	}
	ref, err := requestRef(c.Request)
	if err != nil {
		return abort(http.StatusBadRequest, "invalid JSON: "+err.Error())
	}
	if ref == "" {
		return abort(http.StatusBadRequest, "ref is required：API 令牌不支持批量操作")
	}
	acc, err := m.db.ResolveAccount(c.Request.Context(), ref)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return abort(http.StatusNotFound, "account not found: "+ref)
		}
		return abort(http.StatusInternalServerError, err.Error())
	}
	if acc.ID != p.accountID {
		return abort(http.StatusForbidden, "API 令牌与目标账号不匹配")
	}
	return true
}

// requestRef 从查询参数或 JSON 请求体中读取 ref，读取后会还原请求体供后续处理器使用。
func requestRef(r *http.Request) (string, error) {
	if ref := strings.TrimSpace(r.URL.Query().Get("ref")); ref != "" {
		return ref, nil
	}
	if r.Body == nil {
		return "", nil
	}
	raw, err := io.ReadAll(r.Body)
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(raw))
	if err != nil {
		return "", err
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return "", nil
	}
	var body struct {
		Ref string `json:"ref"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return "", err
	}
	return strings.TrimSpace(body.Ref), nil
}

// isBrowserPagePath 判断是否为浏览器导航页面（跳转登录页而非返回 401）。
func isBrowserPagePath(path string) bool {
	return path == "/" || path == "/scan" || path == "/docs" || strings.HasPrefix(path, "/docs/")
}

func (m *authManager) currentPrincipal(r *http.Request) (principal, bool) {
	if username, ok := m.currentCookieUsername(r); ok {
		return principal{username: username}, true
	}
	value := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(value) < 8 || !strings.EqualFold(value[:7], "Bearer ") {
		return principal{}, false
	}
	token := strings.TrimSpace(value[7:])
	if token == "" {
		return principal{}, false
	}
	username, accountID, ok, err := m.db.ValidateAPIToken(r.Context(), hashAPIToken(token))
	if err != nil || !ok {
		return principal{}, false
	}
	return principal{username: username, accountID: accountID, viaToken: true}, true
}

func (m *authManager) currentUsername(r *http.Request) (string, bool) {
	p, ok := m.currentPrincipal(r)
	return p.username, ok
}

func (m *authManager) currentCookieUsername(r *http.Request) (string, bool) {
	c, err := r.Cookie(m.cookieName)
	if err != nil || c.Value == "" {
		return "", false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	sess, ok := m.sessions[c.Value]
	if !ok || time.Now().After(sess.expiresAt) {
		if ok {
			delete(m.sessions, c.Value)
		}
		return "", false
	}
	// 滑动续期
	sess.expiresAt = time.Now().Add(m.ttl)
	return sess.username, true
}

func hashAPIToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (m *authManager) createSession(username string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pruneLocked()
	m.sessions[token] = &authSession{
		username:  username,
		expiresAt: time.Now().Add(m.ttl),
	}
	return token, nil
}

func (m *authManager) destroySession(token string) {
	if token == "" {
		return
	}
	m.mu.Lock()
	delete(m.sessions, token)
	m.mu.Unlock()
}

func (m *authManager) destroySessionsExceptCurrent(r *http.Request) {
	current := ""
	if c, err := r.Cookie(m.cookieName); err == nil {
		current = c.Value
	}
	m.mu.Lock()
	for token := range m.sessions {
		if token != current {
			delete(m.sessions, token)
		}
	}
	m.mu.Unlock()
}

func (m *authManager) pruneLocked() {
	now := time.Now()
	for token, sess := range m.sessions {
		if now.After(sess.expiresAt) {
			delete(m.sessions, token)
		}
	}
}

func (m *authManager) sessionCookie(r *http.Request, token string, ttl time.Duration) *http.Cookie {
	return &http.Cookie{
		Name:     m.cookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(ttl / time.Second),
	}
}

const passwordAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"

func randomPassword(n int) (string, error) {
	out := make([]byte, n)
	max := big.NewInt(int64(len(passwordAlphabet)))
	for i := range out {
		idx, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		out[i] = passwordAlphabet[idx.Int64()]
	}
	return string(out), nil
}
