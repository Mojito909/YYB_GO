package httpapi

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	// 改密后销毁其他会话，仅保留当前会话
	m.destroySessionsExceptCurrent(r)
	writeJSON(w, http.StatusOK, map[string]any{"password_changed": true})
}

// ginMiddleware 校验会话：API 请求未认证返回 401，页面请求跳转 /login。
func (m *authManager) ginMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := m.currentUsername(c.Request); ok {
			c.Next()
			return
		}
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
	}
}

// isBrowserPagePath 判断是否为浏览器导航页面（跳转登录页而非返回 401）。
func isBrowserPagePath(path string) bool {
	return path == "/" || path == "/scan" || path == "/docs" || strings.HasPrefix(path, "/docs/")
}

func (m *authManager) currentUsername(r *http.Request) (string, bool) {
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
