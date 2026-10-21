package httpapi

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	httpSwagger "github.com/swaggo/http-swagger/v2"

	"yyb_go/internal/notify"
	"yyb_go/internal/protocol"
	"yyb_go/internal/qr"
	"yyb_go/internal/store"
)

type Config struct {
	ResourceRoot   string
	DataRoot       string
	DBFilename     string
	TCPProxy       string
	BarkServer     string
	TrustProxy     bool
	SecureCookie   bool
	SessionTTL     time.Duration
	RequestTimeout time.Duration
	AvatarTimeout  time.Duration
	QRSessionTTL   time.Duration
	AuthTTL        time.Duration
}

const (
	accessLogEnv        = "YYB_ACCESS_LOG"
	maxRequestBodyBytes = 1 << 20
)

type App struct {
	cfg       Config
	resources resources
	db        *store.DB
	pool      *protocol.Pool
	qr        *qr.Client
	auth      *authManager
	apiLogs   *apiLogWriter
	notifier  *notify.Bark
	startedAt time.Time

	mu         sync.Mutex
	qrSessions map[string]*qr.Session
}

var swaggerDocsHandler = httpSwagger.Handler(
	httpSwagger.URL("/openapi.json"),
	httpSwagger.DocExpansion("list"),
	httpSwagger.DeepLinking(true),
	httpSwagger.DefaultModelsExpandDepth(httpSwagger.ShowModel),
)

func NewApp(cfg Config) (*App, error) {
	if cfg.ResourceRoot == "" {
		cfg.ResourceRoot = filepath.Join(".", "resource")
	}
	if cfg.DBFilename == "" {
		cfg.DBFilename = DefaultDBFilename
	}
	if cfg.RequestTimeout == 0 {
		cfg.RequestTimeout = 8 * time.Second
	}
	if cfg.AvatarTimeout == 0 {
		cfg.AvatarTimeout = 10 * time.Second
	}
	if cfg.SessionTTL == 0 {
		cfg.SessionTTL = 30 * time.Minute
	}
	if cfg.QRSessionTTL == 0 {
		cfg.QRSessionTTL = 5 * time.Minute
	}
	if cfg.AuthTTL == 0 {
		cfg.AuthTTL = 24 * time.Hour
	}
	res, err := ensureResources(cfg.ResourceRoot, cfg.DataRoot)
	if err != nil {
		return nil, err
	}
	dbPath, err := prepareDBPath(res.DB, cfg.DBFilename)
	if err != nil {
		return nil, err
	}
	db, err := store.Open(dbPath)
	if err != nil {
		return nil, err
	}
	auth := newAuthManager(db, cfg.AuthTTL, cfg.TrustProxy, cfg.SecureCookie)
	if err := auth.bootstrap(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	notifier, err := notify.NewBark(cfg.BarkServer, barkSendTimeout)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	poolCfg := protocol.DefaultConfig()
	poolCfg.SessionTTL = cfg.SessionTTL
	poolCfg.ShortlinkTimeout = cfg.RequestTimeout
	poolCfg.TCPProxy = cfg.TCPProxy
	pool := protocol.NewPool(poolCfg, db)
	return &App{
		cfg:        cfg,
		resources:  res,
		db:         db,
		pool:       pool,
		qr:         qr.NewClient(cfg.RequestTimeout),
		startedAt:  time.Now(),
		auth:       auth,
		apiLogs:    newAPILogWriter(db),
		notifier:   notifier,
		qrSessions: map[string]*qr.Session{},
	}, nil
}

func (a *App) Close() error {
	a.mu.Lock()
	sessions := make([]*qr.Session, 0, len(a.qrSessions))
	for _, sess := range a.qrSessions {
		sessions = append(sessions, sess)
	}
	a.qrSessions = map[string]*qr.Session{}
	a.mu.Unlock()
	for _, sess := range sessions {
		sess.StopWatch()
	}
	if a.apiLogs != nil {
		a.apiLogs.Close()
	}
	if a.db != nil {
		return a.db.Close()
	}
	return nil
}

func (a *App) Handler() http.Handler {
	if os.Getenv(gin.EnvGinMode) == "" {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.New()
	// 未显式开启 trust-proxy 时，gin 不应信任任何转发头。
	if a.cfg.TrustProxy {
		_ = router.SetTrustedProxies([]string{"0.0.0.0/0", "::/0"})
	} else {
		_ = router.SetTrustedProxies(nil)
	}
	if middleware := accessLogger(strings.ToLower(strings.TrimSpace(os.Getenv(accessLogEnv)))); middleware != nil {
		router.Use(middleware)
	}
	router.Use(gin.Recovery())
	router.Use(bodyLimitMiddleware(maxRequestBodyBytes))

	// 公开路由：登录页、鉴权接口、健康检查、静态资源
	router.Any("/login", gin.WrapF(a.handleLoginPage))
	router.Any("/auth/login", gin.WrapF(a.auth.handleLogin))
	router.Any("/auth/logout", gin.WrapF(a.auth.handleLogout))
	router.Any("/auth/me", gin.WrapF(a.auth.handleMe))
	router.Any("/auth/password", gin.WrapF(a.auth.handleChangePassword))
	router.Any("/auth/token", gin.WrapF(a.auth.handleToken))
	router.Any("/health", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		if err := a.db.Ping(ctx); err != nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, apiEnvelope{
				Code: http.StatusServiceUnavailable,
				Msg:  "database unavailable",
				Data: nil,
			})
			return
		}
		writeJSON(c.Writer, http.StatusOK, gin.H{"ok": true})
	})
	// 静态资源禁用强缓存，避免前端改动后浏览器仍使用旧脚本
	static := router.Group("/static", func(c *gin.Context) {
		c.Header("Cache-Control", "no-cache")
	})
	static.StaticFS("/", http.Dir(a.resources.Static))

	// 受保护路由：页面与全部业务 API
	protected := router.Group("/", a.auth.ginMiddleware(), a.apiAuditMiddleware())
	protected.Any("/", gin.WrapF(a.handleIndex))
	protected.Any("/scan", gin.WrapF(a.handleScan))
	protected.Any("/docs", func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, "/docs/index.html")
	})
	protected.Any("/docs/*path", gin.WrapF(a.handleDocs))
	protected.Any("/openapi.json", gin.WrapF(a.handleOpenAPI))
	protected.Any("/qr", gin.WrapF(a.handleQRRoot))
	protected.Any("/qr/*path", gin.WrapF(a.handleQR))
	protected.Any("/accounts", gin.WrapF(a.handleAccountsRoot))
	protected.Any("/accounts/avatar", gin.WrapF(a.handleAccountAvatar))
	protected.Any("/accounts/bark", gin.WrapF(a.handleAccountBark))
	protected.Any("/accounts/bark/test", gin.WrapF(a.handleAccountBarkTest))
	protected.Any("/accounts/refresh", gin.WrapF(a.handleAccountRefresh))
	protected.Any("/accounts/resync", gin.WrapF(a.handleAccountResync))
	protected.Any("/wxapp/getCode", gin.WrapF(a.handleGetCode))
	protected.Any("/wxapp/getPhoneNumber", gin.WrapF(a.handleGetPhoneNumber))
	protected.Any("/wxapp/operateWxData", gin.WrapF(a.handleOperateWXData))
	protected.Any("/api-logs", gin.WrapF(a.handleAPILogs))
	protected.Any("/api-logs/settings", gin.WrapF(a.handleAPILogSettings))
	protected.Any("/api-logs/:id", gin.WrapF(a.handleAPILogByID))
	protected.Any("/dashboard/metrics", gin.WrapF(a.handleDashboardMetrics))
	router.NoRoute(func(c *gin.Context) {
		writeError(c.Writer, http.StatusNotFound, "not found")
	})

	return router
}

// bodyLimitMiddleware 限制请求体大小：声明长度超限直接 413，
// 其余情况用 MaxBytesReader 兜底（例如 chunked 请求）。
func bodyLimitMiddleware(max int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.ContentLength > max {
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, apiEnvelope{
				Code: http.StatusRequestEntityTooLarge,
				Msg:  "request body too large",
				Data: nil,
			})
			return
		}
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, max)
		}
		c.Next()
	}
}

func accessLogger(mode string) gin.HandlerFunc {
	switch mode {
	case "off":
		return nil
	case "errors":
		return func(c *gin.Context) {
			started := time.Now()
			c.Next()
			duration := time.Since(started)
			status := c.Writer.Status()
			if status < http.StatusInternalServerError && duration < 2*time.Second {
				return
			}
			log.Printf("[http] %s %s status=%d duration=%s ip=%s", c.Request.Method, c.Request.URL.Path, status, duration.Round(time.Millisecond), c.ClientIP())
		}
	default:
		return gin.Logger()
	}
}

func (a *App) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/login" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if _, ok := a.auth.currentUsername(r); ok {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	serveFileOrText(w, r, filepath.Join(a.resources.Templates, "login.html"), fallbackLoginHTML)
}

func (a *App) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	serveFileOrText(w, r, filepath.Join(a.resources.Templates, "index.html"), fallbackIndexHTML)
}

func (a *App) handleScan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	serveFileOrText(w, r, filepath.Join(a.resources.Templates, "scan.html"), fallbackScanHTML)
}

func (a *App) handleDocs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if r.URL.Path == "/docs/" {
		http.Redirect(w, r, "/docs/index.html", http.StatusMovedPermanently)
		return
	}
	swaggerDocsHandler.ServeHTTP(w, r)
}

func (a *App) handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	writeRawJSON(w, http.StatusOK, openAPISpec)
}

func (a *App) handleQRRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/qr" {
		writeError(w, http.StatusNotFound, "qr session not found")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	a.pruneQR()
	ctx, cancel := context.WithTimeout(r.Context(), a.cfg.RequestTimeout+35*time.Second)
	defer cancel()
	img, err := a.qr.GetQRCodeImage(ctx)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	a.mu.Lock()
	a.qrSessions[img.Session.ID] = img.Session
	keep := make(map[string]bool, len(a.qrSessions))
	for sid := range a.qrSessions {
		keep[sid] = true
	}
	a.mu.Unlock()
	// 后台持续代理微信长轮询，/qr/{id}/poll 只读缓存状态，避免请求被挂住 25s+
	a.qr.StartWatch(img.Session)
	path := a.resources.qrPath(img.Session.ID)
	_ = os.WriteFile(path, img.ImageBytes, 0o644)
	a.cleanupQR(keep)
	out := map[string]any{
		"session_id": img.Session.ID,
		// watcher 已在并发写状态，必须通过快照读取
		"status":    img.Session.Snapshot().Status,
		"image_url": "/qr/" + img.Session.ID + "/image",
	}
	if r.URL.Query().Get("as_base64") == "true" {
		out["image_base64"] = qr.DataURIJPEG(img.ImageBytes)
	} else {
		out["image_base64"] = nil
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *App) handleQR(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/qr/"), "/")
	if len(parts) != 2 {
		writeError(w, http.StatusNotFound, "qr session not found")
		return
	}
	sessionID, action := parts[0], parts[1]
	switch action {
	case "image":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		path := a.resources.qrPath(sessionID)
		if _, err := os.Stat(path); err != nil {
			writeError(w, http.StatusNotFound, "qr session not found")
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		http.ServeFile(w, r, path)
	case "poll":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		sess := a.getQRSession(sessionID)
		if sess == nil {
			writeError(w, http.StatusNotFound, "qr session not found")
			return
		}
		// 读后台 watcher 维护的状态快照，毫秒级返回
		result := sess.Snapshot()
		if terminalQR(result.Status) {
			a.dropQRSession(sessionID)
		}
		writeJSON(w, http.StatusOK, result)
	case "confirm":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		sess := a.getQRSession(sessionID)
		if sess == nil {
			writeError(w, http.StatusNotFound, "qr session not found")
			return
		}
		result, err := a.qr.GetLoginBuffer(r.Context(), sess)
		if err != nil {
			writeError(w, http.StatusConflict, "buffer not ready: "+err.Error())
			return
		}
		var userInfo map[string]any
		if ui, err := a.qr.LoginBuffers().FetchUserInfo(r.Context(), result.Credentials); err == nil {
			userInfo = ui
		}
		acc, err := a.storeFromScan(r.Context(), result.LoginBuffer, result.Credentials, userInfo)
		if err != nil {
			writeInternalError(w, err)
			return
		}
		a.dropQRSession(sessionID)
		writeJSON(w, http.StatusOK, acc.Public())
	default:
		writeError(w, http.StatusNotFound, "qr session not found")
	}
}

func (a *App) handleAccountsRoot(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/accounts" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		// 账号级 API 令牌只能看到自己绑定的账号
		if p, ok := principalFrom(r.Context()); ok && p.viaToken {
			acc, err := a.db.GetAccount(r.Context(), p.accountID)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					writeJSON(w, http.StatusOK, []store.AccountPublic{})
					return
				}
				writeInternalError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, []store.AccountPublic{acc.Public()})
			return
		}
		accounts, err := a.db.ListAccounts(r.Context())
		if err != nil {
			writeInternalError(w, err)
			return
		}
		out := make([]store.AccountPublic, 0, len(accounts))
		for _, acc := range accounts {
			out = append(out, acc.Public())
		}
		writeJSON(w, http.StatusOK, out)
	case http.MethodDelete:
		acc, ok := a.resolveAccountFromQuery(w, r)
		if !ok {
			return
		}
		if err := a.db.DeleteAccount(r.Context(), acc.ID); err != nil {
			writeInternalError(w, err)
			return
		}
		a.removeAccountAvatar(acc)
		writeJSON(w, http.StatusOK, map[string]any{"deleted": acc.ID, "openid": acc.OpenID})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (a *App) handleAccountAvatar(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/accounts/avatar" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	acc, ok := a.resolveAccountFromQuery(w, r)
	if !ok {
		return
	}
	a.serveAvatar(w, r, acc)
}

func (a *App) handleAccountRefresh(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/accounts/refresh" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body accountRefIn
	if err := decodeOptionalJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.Ref == "" {
		a.refreshAll(w, r)
		return
	}
	acc, ok := a.resolveAccountRef(w, r, body.Ref)
	if !ok {
		return
	}
	status := a.refreshLiveness(r.Context(), acc)
	writeJSON(w, http.StatusOK, refreshOut(acc, status))
}

func (a *App) handleAccountResync(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/accounts/resync" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body accountRefIn
	if err := decodeOptionalJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.Ref == "" {
		a.resyncAll(w, r)
		return
	}
	acc, ok := a.resolveAccountRef(w, r, body.Ref)
	if !ok {
		return
	}
	updated, err := a.resyncProfile(r.Context(), acc)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, updated.Public())
}

func (a *App) handleGetCode(w http.ResponseWriter, r *http.Request) {
	if !acceptWXAppRoute(w, r, "/wxapp/getCode") {
		return
	}
	a.callWXApp(w, r, false, a.invokeGetCode)
}

func (a *App) handleGetPhoneNumber(w http.ResponseWriter, r *http.Request) {
	if !acceptWXAppRoute(w, r, "/wxapp/getPhoneNumber") {
		return
	}
	a.callWXApp(w, r, false, a.invokeGetPhoneNumber)
}

func (a *App) handleOperateWXData(w http.ResponseWriter, r *http.Request) {
	if !acceptWXAppRoute(w, r, "/wxapp/operateWxData") {
		return
	}
	a.callWXApp(w, r, true, a.invokeOperateWXData)
}

func acceptWXAppRoute(w http.ResponseWriter, r *http.Request, path string) bool {
	if r.URL.Path != path {
		writeError(w, http.StatusNotFound, "not found")
		return false
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return false
	}
	return true
}

type accountRefIn struct {
	Ref string `json:"ref"`
}

type wxappRequest struct {
	Ref     string         `json:"ref"`
	AppID   string         `json:"app_id"`
	Payload map[string]any `json:"payload"`
}

type wxappCall func(ctx context.Context, acc *store.WechatAccount, appID string, payload map[string]any) (map[string]any, error)

func (a *App) callWXApp(w http.ResponseWriter, r *http.Request, requirePayload bool, call wxappCall) {
	var body wxappRequest
	if err := decodeOptionalJSON(r, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}
	if body.Ref == "" {
		writeError(w, http.StatusBadRequest, "ref is required")
		return
	}
	if body.AppID == "" {
		writeError(w, http.StatusBadRequest, "app_id is required")
		return
	}
	if requirePayload && body.Payload == nil {
		writeError(w, http.StatusBadRequest, "payload is required")
		return
	}
	acc, ok := a.resolveAccountRef(w, r, body.Ref)
	if !ok {
		return
	}
	result, err := a.invokeWXApp(r.Context(), acc, body.AppID, body.Payload, call)
	if err != nil {
		var expired accountExpiredError
		switch {
		case errors.As(err, &expired):
			writeError(w, http.StatusConflict, "account login_buffer expired (refresh failed); re-scan required")
		default:
			writeError(w, http.StatusBadGateway, "call failed: "+err.Error())
		}
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"openid": acc.OpenID, "result": result})
}

func decodeOptionalJSON(r *http.Request, dst any) error {
	err := json.NewDecoder(r.Body).Decode(dst)
	if errors.Is(err, io.EOF) {
		return nil
	}
	return err
}

func (a *App) resolveAccountFromQuery(w http.ResponseWriter, r *http.Request) (*store.WechatAccount, bool) {
	ref := strings.TrimSpace(r.URL.Query().Get("ref"))
	if ref == "" {
		writeError(w, http.StatusBadRequest, "ref query param is required")
		return nil, false
	}
	return a.resolveAccountRef(w, r, ref)
}

func (a *App) resolveAccountRef(w http.ResponseWriter, r *http.Request, ref string) (*store.WechatAccount, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		writeError(w, http.StatusBadRequest, "ref is required")
		return nil, false
	}
	acc, err := a.db.ResolveAccount(r.Context(), ref)
	if err != nil {
		if errors.Is(err, store.ErrAmbiguousRef) {
			writeError(w, http.StatusBadRequest, "ref 同时匹配多个账号，请使用 id: / uin: / openid: 前缀")
			return nil, false
		}
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "account not found: "+ref)
		} else {
			writeInternalError(w, err)
		}
		return nil, false
	}
	return acc, true
}

func (a *App) refreshAll(w http.ResponseWriter, r *http.Request) {
	accounts, err := a.db.ListAccounts(r.Context())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	out := make([]map[string]any, 0, len(accounts))
	for _, acc := range accounts {
		out = append(out, refreshOut(acc, a.refreshLiveness(r.Context(), acc)))
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *App) resyncAll(w http.ResponseWriter, r *http.Request) {
	accounts, err := a.db.ListAccounts(r.Context())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	out := make([]store.AccountPublic, 0, len(accounts))
	for _, acc := range accounts {
		updated, err := a.resyncProfile(r.Context(), acc)
		if err != nil {
			writeInternalError(w, err)
			return
		}
		out = append(out, updated.Public())
	}
	writeJSON(w, http.StatusOK, out)
}

// removeAccountAvatar 删除账号绑定的本地头像文件，避免删除账号后残留。
func (a *App) removeAccountAvatar(acc *store.WechatAccount) {
	if acc.Avatar == nil || *acc.Avatar == "" {
		return
	}
	base := filepath.Clean(a.resources.Avatars) + string(os.PathSeparator)
	path := filepath.Clean(*acc.Avatar)
	if !strings.HasPrefix(path, base) {
		return
	}
	_ = os.Remove(path)
}

func (a *App) serveAvatar(w http.ResponseWriter, r *http.Request, acc *store.WechatAccount) {
	if acc.Avatar != nil && *acc.Avatar != "" {
		if _, err := os.Stat(*acc.Avatar); err == nil {
			w.Header().Set("Content-Type", "image/jpeg")
			http.ServeFile(w, r, *acc.Avatar)
			return
		}
		if strings.HasPrefix(*acc.Avatar, "http://") || strings.HasPrefix(*acc.Avatar, "https://") {
			http.Redirect(w, r, *acc.Avatar, http.StatusFound)
			return
		}
	}
	writeError(w, http.StatusNotFound, "no avatar")
}

func (a *App) storeFromScan(ctx context.Context, loginBuffer string, creds protocol.LoginBufferCredentials, userInfo map[string]any) (*store.WechatAccount, error) {
	openid := creds.OpenID
	wasExpired := false
	if prev, err := a.db.GetAccountByOpenID(ctx, openid); err == nil {
		wasExpired = prev.Status != nil && *prev.Status == "expired"
	}
	nick := pickNickname(userInfo, creds.Nickname)
	avatar := a.resolveAvatar(ctx, openid, userInfo)
	status := "alive"
	acc, err := a.db.UpsertAccount(ctx, openid, loginBuffer, stringPtrMaybe(nick), stringPtrMaybe(nick), stringPtrMaybe(avatar), userInfo, creds.ToMap(), &status)
	if err != nil {
		return nil, err
	}
	// 重新扫码是失效账号最常见的恢复方式，恢复成功时同样推送一次通知。
	if wasExpired {
		if claimed, err := a.db.ClaimAccountRecoveryNotification(ctx, acc.ID); err == nil && claimed {
			a.notifyAccountRecovered(acc)
		}
	}
	return acc, nil
}

func (a *App) refreshLiveness(ctx context.Context, acc *store.WechatAccount) string {
	if acc.Credentials == nil {
		_ = a.db.SetAccountStatus(ctx, acc.ID, "unknown")
		return "unknown"
	}
	creds := protocol.CredentialsFromMap(acc.Credentials)
	result, err := a.qr.RefreshLoginBuffer(ctx, creds)
	if err != nil {
		var credErr *protocol.CredentialError
		if errors.As(err, &credErr) {
			_ = a.db.SetAccountStatus(ctx, acc.ID, "expired")
			a.notifyAccountExpired(acc)
			return "expired"
		}
		// 网络抖动或上游临时故障不应把账号标记为失效，保留原状态。
		if acc.Status != nil && *acc.Status != "" {
			return *acc.Status
		}
		return "unknown"
	}
	_ = a.db.SetAccountCredential(ctx, acc.ID, result.LoginBuffer, result.Credentials.ToMap())
	recovered, err := a.db.SetAccountAlive(ctx, acc.ID)
	if err == nil && recovered {
		a.notifyAccountRecovered(acc)
	}
	if avatar := a.resolveAvatar(ctx, acc.OpenID, acc.UserInfo); avatar != "" {
		_ = a.db.SetAccountProfile(ctx, acc.ID, acc.Nickname, &avatar, acc.UserInfo)
	}
	return "alive"
}

func (a *App) resyncProfile(ctx context.Context, acc *store.WechatAccount) (*store.WechatAccount, error) {
	nick := pickNickname(acc.UserInfo, deref(acc.Nickname))
	avatar := a.resolveAvatar(ctx, acc.OpenID, acc.UserInfo)
	if avatar == "" {
		avatar = deref(acc.Avatar)
	}
	if err := a.db.SetAccountProfile(ctx, acc.ID, stringPtrMaybe(nick), stringPtrMaybe(avatar), acc.UserInfo); err != nil {
		return nil, err
	}
	return a.db.GetAccount(ctx, acc.ID)
}

type accountExpiredError struct{ openid string }

func (e accountExpiredError) Error() string { return "account expired: " + e.openid }

func (a *App) invokeWXApp(ctx context.Context, acc *store.WechatAccount, appID string, payload map[string]any, call wxappCall) (map[string]any, error) {
	proxy := a.cfg.TCPProxy
	if _, err := a.db.GetSession(ctx, acc.ID, proxy); err == nil {
		result, err := call(ctx, acc, appID, payload)
		if err == nil {
			return result, nil
		}
		_ = a.db.InvalidateSession(ctx, acc.ID, proxy)
	}
	status := a.refreshLiveness(ctx, acc)
	if status != "alive" {
		if status == "expired" {
			return nil, accountExpiredError{openid: acc.OpenID}
		}
		return nil, fmt.Errorf("account refresh failed temporarily, retry later")
	}
	fresh, err := a.db.GetAccount(ctx, acc.ID)
	if err == nil && fresh != nil {
		acc = fresh
	}
	return call(ctx, acc, appID, payload)
}

func (a *App) invokeGetCode(ctx context.Context, acc *store.WechatAccount, appID string, _ map[string]any) (map[string]any, error) {
	return a.pool.GetCode(ctx, acc.LoginBuffer, appID, acc.ID, a.cfg.TCPProxy)
}

func (a *App) invokeGetPhoneNumber(ctx context.Context, acc *store.WechatAccount, appID string, _ map[string]any) (map[string]any, error) {
	return a.pool.GetPhoneNumber(ctx, acc.LoginBuffer, appID, acc.ID, a.cfg.TCPProxy)
}

func (a *App) invokeOperateWXData(ctx context.Context, acc *store.WechatAccount, appID string, payload map[string]any) (map[string]any, error) {
	return a.pool.OperateWXData(ctx, acc.LoginBuffer, appID, payload, acc.ID, a.cfg.TCPProxy)
}

func refreshOut(acc *store.WechatAccount, status string) map[string]any {
	return map[string]any{"id": acc.ID, "openid": acc.OpenID, "uin": acc.UIN, "nickname": acc.Nickname, "status": status}
}

func pickNickname(userInfo map[string]any, fallback string) string {
	if s := stringFromAny(userInfo["nick_name"]); s != "" {
		return s
	}
	return fallback
}

func pickAvatarURL(userInfo map[string]any) string {
	for _, k := range []string{"head_img_url", "head_url", "headimgurl", "avatar"} {
		if s := stringFromAny(userInfo[k]); s != "" {
			return s
		}
	}
	return ""
}

func (a *App) resolveAvatar(ctx context.Context, openid string, userInfo map[string]any) string {
	u := pickAvatarURL(userInfo)
	if u == "" {
		return ""
	}
	dest := a.resources.avatarPath(openid)
	if downloadAvatar(ctx, u, dest, a.cfg.AvatarTimeout) {
		return dest
	}
	return u
}

// avatarHTTPClient 只用于下载头像：不跟随环境代理，并在拨号阶段拒绝内网地址。
var avatarHTTPClient = &http.Client{
	Transport: &http.Transport{
		DialContext:           safeDialContext,
		TLSHandshakeTimeout:   5 * time.Second,
		ResponseHeaderTimeout: 5 * time.Second,
	},
}

func safeDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	var lastErr error
	for _, ip := range ips {
		if isBlockedIP(ip.IP) {
			continue
		}
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("avatar host resolves to non-public address: %s", host)
	}
	return nil, lastErr
}

// isBlockedIP 拦截回环、私网、链路本地、组播以及 CGNAT 地址，避免 SSRF。
func isBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}
	// 100.64.0.0/10（含云厂商元数据地址 100.100.100.200）
	if ip4 := ip.To4(); ip4 != nil && ip4[0] == 100 && ip4[1] >= 64 && ip4[1] <= 127 {
		return true
	}
	return false
}

func downloadAvatar(ctx context.Context, url, dest string, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := avatarHTTPClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil || resp.StatusCode != 200 || !looksLikeImage(data) {
		return false
	}
	_ = os.MkdirAll(filepath.Dir(dest), 0o755)
	return os.WriteFile(dest, data, 0o644) == nil
}

func looksLikeImage(data []byte) bool {
	if len(data) < 64 {
		return false
	}
	magics := [][]byte{{0xff, 0xd8, 0xff}, {0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}, []byte("GIF87a"), []byte("GIF89a")}
	for _, m := range magics {
		if bytes.HasPrefix(data, m) {
			return true
		}
	}
	return false
}

func (a *App) getQRSession(id string) *qr.Session {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.qrSessions[id]
}

func (a *App) dropQRSession(id string) {
	a.mu.Lock()
	sess := a.qrSessions[id]
	delete(a.qrSessions, id)
	a.mu.Unlock()
	if sess != nil {
		sess.StopWatch()
	}
	_ = os.Remove(a.resources.qrPath(id))
}

func (a *App) pruneQR() {
	a.mu.Lock()
	var drop []string
	var stale []*qr.Session
	for sid, sess := range a.qrSessions {
		if sess.Age() > a.cfg.QRSessionTTL {
			drop = append(drop, sid)
			stale = append(stale, sess)
		}
	}
	for _, sid := range drop {
		delete(a.qrSessions, sid)
	}
	a.mu.Unlock()
	for _, sess := range stale {
		sess.StopWatch()
	}
	for _, sid := range drop {
		_ = os.Remove(a.resources.qrPath(sid))
	}
}

func (a *App) cleanupQR(keep map[string]bool) {
	// 只清理超过会话 TTL 且不在保留集合内的图片，
	// 避免并发创建二维码时删掉另一个请求刚写入的文件。
	cutoff := time.Now().Add(-a.cfg.QRSessionTTL)
	files, _ := filepath.Glob(filepath.Join(a.resources.QR, "*.jpg"))
	for _, f := range files {
		sid := strings.TrimSuffix(filepath.Base(f), ".jpg")
		if keep[sid] {
			continue
		}
		info, err := os.Stat(f)
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		_ = os.Remove(f)
	}
}

func terminalQR(status string) bool {
	return status == "expired" || status == "cancelled" || status == "unknown"
}

type apiEnvelope struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data any    `json:"data"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	writeRawJSON(w, status, apiEnvelope{
		Code: 0,
		Msg:  "success",
		Data: v,
	})
}

func writeRawJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, status int, detail string) {
	writeRawJSON(w, status, apiEnvelope{
		Code: status,
		Msg:  detail,
		Data: nil,
	})
}

// writeInternalError 把内部错误写入日志，但只向客户端返回通用提示。
func writeInternalError(w http.ResponseWriter, err error) {
	log.Printf("[http] internal error: %v", err)
	writeError(w, http.StatusInternalServerError, "internal server error")
}

func serveFileOrText(w http.ResponseWriter, r *http.Request, path, fallback string) {
	if _, err := os.Stat(path); err == nil {
		http.ServeFile(w, r, path)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(fallback))
}

func stringFromAny(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func stringPtrMaybe(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func safeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		}
	}
	return b.String()
}
