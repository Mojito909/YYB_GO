package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testAdminUser = "admin"
const testAdminPass = "test-password-123"

func newTestApp(t *testing.T) (*App, http.Handler) {
	t.Helper()
	t.Setenv("GIN_MODE", "test")
	t.Setenv(accessLogEnv, "off")
	t.Setenv("YYB_ADMIN_USER", testAdminUser)
	t.Setenv("YYB_ADMIN_PASSWORD", testAdminPass)

	resourceRoot := t.TempDir()
	// 准备一个静态资源文件，供 /static 路由测试使用
	staticDir := filepath.Join(resourceRoot, "static", "css")
	if err := os.MkdirAll(staticDir, 0o755); err != nil {
		t.Fatalf("mkdir static dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(staticDir, "base.css"), []byte("/* test */"), 0o644); err != nil {
		t.Fatalf("write base.css: %v", err)
	}

	app, err := NewApp(Config{
		ResourceRoot:   resourceRoot,
		RequestTimeout: time.Second,
		AvatarTimeout:  time.Second,
		SessionTTL:     time.Minute,
		QRSessionTTL:   time.Minute,
		AuthTTL:        time.Minute,
	})
	if err != nil {
		t.Fatalf("NewApp() error = %v", err)
	}
	t.Cleanup(func() { _ = app.Close() })
	return app, app.Handler()
}

// loginForTest 登录并返回会话 cookie。
func loginForTest(t *testing.T, handler http.Handler, user, pass string) *http.Cookie {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"username": user, "password": pass})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == authCookieName {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatalf("login response missing session cookie")
	}
	return cookie
}

func authedRequest(handler http.Handler, cookie *http.Cookie, method, path string, body []byte) *httptest.ResponseRecorder {
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, path, bytes.NewReader(body))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// bearerRequest 用账号级 API 令牌发起请求。
func bearerRequest(handler http.Handler, token, method, path string, body []byte) *httptest.ResponseRecorder {
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, path, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

// issueTokenForTest 通过 Cookie 会话为指定账号生成令牌，返回明文令牌。
func issueTokenForTest(t *testing.T, handler http.Handler, cookie *http.Cookie, ref string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"ref": ref})
	rec := authedRequest(handler, cookie, http.MethodPost, "/auth/token", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /auth/token ref=%s status = %d, body = %s", ref, rec.Code, rec.Body.String())
	}
	var parsed struct {
		Data struct {
			Token     string `json:"token"`
			AccountID int64  `json:"account_id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("decode token response: %v", err)
	}
	if parsed.Data.Token == "" || parsed.Data.AccountID == 0 {
		t.Fatalf("token response missing token or account_id: %s", rec.Body.String())
	}
	return parsed.Data.Token
}

func TestHandlerServesGinRoutesAndSwaggerDocs(t *testing.T) {
	_, handler := newTestApp(t)
	cookie := loginForTest(t, handler, testAdminUser, testAdminPass)

	health := authedRequest(handler, nil, http.MethodGet, "/health", nil)
	if health.Code != http.StatusOK {
		t.Fatalf("GET /health status = %d", health.Code)
	}
	var healthBody struct {
		Code int            `json:"code"`
		Msg  string         `json:"msg"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(health.Body.Bytes(), &healthBody); err != nil {
		t.Fatalf("decode health JSON: %v", err)
	}
	if healthBody.Code != 0 || healthBody.Msg != "success" || healthBody.Data["ok"] != true {
		t.Fatalf("GET /health body = %#v", healthBody)
	}

	openapi := authedRequest(handler, cookie, http.MethodGet, "/openapi.json", nil)
	if openapi.Code != http.StatusOK {
		t.Fatalf("GET /openapi.json status = %d", openapi.Code)
	}
	var spec map[string]any
	if err := json.Unmarshal(openapi.Body.Bytes(), &spec); err != nil {
		t.Fatalf("decode OpenAPI JSON: %v", err)
	}
	if spec["openapi"] != "3.0.3" {
		t.Fatalf("openapi version = %v", spec["openapi"])
	}
	if _, ok := spec["code"]; ok {
		t.Fatalf("OpenAPI JSON should not be wrapped in API envelope")
	}
	paths, ok := spec["paths"].(map[string]any)
	if !ok {
		t.Fatalf("OpenAPI paths missing or invalid")
	}
	for _, path := range []string{"/auth/login", "/auth/logout", "/auth/me", "/auth/password", "/auth/token", "/wxapp/getCode", "/wxapp/getPhoneNumber", "/wxapp/operateWxData", "/accounts/avatar", "/dashboard/metrics"} {
		if _, ok := paths[path]; !ok {
			t.Fatalf("OpenAPI path %s missing", path)
		}
	}
	dashboard := authedRequest(handler, cookie, http.MethodGet, "/dashboard/metrics?period=day", nil)
	if dashboard.Code != http.StatusOK {
		t.Fatalf("GET /dashboard/metrics status = %d, body = %s", dashboard.Code, dashboard.Body.String())
	}
	var dashboardBody struct {
		Data struct {
			Metrics struct {
				Period  string `json:"period"`
				Buckets []any  `json:"buckets"`
			} `json:"metrics"`
		} `json:"data"`
	}
	if err := json.Unmarshal(dashboard.Body.Bytes(), &dashboardBody); err != nil {
		t.Fatalf("decode dashboard metrics: %v", err)
	}
	if dashboardBody.Data.Metrics.Period != "day" || len(dashboardBody.Data.Metrics.Buckets) != 24 {
		t.Fatalf("dashboard metrics = %#v", dashboardBody.Data.Metrics)
	}
	for _, path := range []string{"/wxapp/getCode", "/wxapp/getPhoneNumber", "/wxapp/operateWxData"} {
		pathItem := paths[path].(map[string]any)
		post := pathItem["post"].(map[string]any)
		tags := post["tags"].([]any)
		if len(tags) != 1 || tags[0] != "wxapp" {
			t.Fatalf("OpenAPI path %s tags = %#v, want [wxapp]", path, tags)
		}
	}
	for _, path := range []string{"/accounts/{ref}", "/accounts/{ref}/getCode", "/accounts/{ref}/getPhoneNumber", "/accounts/{ref}/operateWxData", "/accounts/getCode", "/accounts/getPhoneNumber", "/accounts/operateWxData"} {
		if _, ok := paths[path]; ok {
			t.Fatalf("OpenAPI still exposes old account feature route %s", path)
		}
	}
	if _, ok := paths["/features"]; ok {
		t.Fatalf("OpenAPI still exposes /features")
	}

	docs := authedRequest(handler, cookie, http.MethodGet, "/docs", nil)
	if docs.Code != http.StatusMovedPermanently {
		t.Fatalf("GET /docs status = %d", docs.Code)
	}
	if got := docs.Header().Get("Location"); got != "/docs/index.html" {
		t.Fatalf("GET /docs Location = %q", got)
	}

	features := authedRequest(handler, cookie, http.MethodGet, "/features", nil)
	if features.Code != http.StatusNotFound {
		t.Fatalf("GET /features status = %d", features.Code)
	}
	var notFoundBody struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data any    `json:"data"`
	}
	if err := json.Unmarshal(features.Body.Bytes(), &notFoundBody); err != nil {
		t.Fatalf("decode /features error JSON: %v", err)
	}
	if notFoundBody.Code == 0 || notFoundBody.Msg == "" || notFoundBody.Data != nil {
		t.Fatalf("GET /features body = %#v", notFoundBody)
	}

	oldPath := authedRequest(handler, cookie, http.MethodPost, "/accounts/getCode", nil)
	if oldPath.Code != http.StatusNotFound {
		t.Fatalf("POST old account feature route status = %d", oldPath.Code)
	}
}

func TestAuthLoginLogoutFlow(t *testing.T) {
	_, handler := newTestApp(t)

	// 错误密码被拒绝
	wrong := authedRequest(handler, nil, http.MethodPost, "/auth/login",
		[]byte(`{"username":"admin","password":"wrong"}`))
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("login with wrong password status = %d", wrong.Code)
	}

	// 正确密码下发会话 cookie
	cookie := loginForTest(t, handler, testAdminUser, testAdminPass)

	// /auth/me 返回当前用户
	me := authedRequest(handler, cookie, http.MethodGet, "/auth/me", nil)
	if me.Code != http.StatusOK {
		t.Fatalf("GET /auth/me status = %d", me.Code)
	}
	if !strings.Contains(me.Body.String(), testAdminUser) {
		t.Fatalf("GET /auth/me body = %s", me.Body.String())
	}

	// 带 cookie 可访问受保护 API
	accounts := authedRequest(handler, cookie, http.MethodGet, "/accounts", nil)
	if accounts.Code != http.StatusOK {
		t.Fatalf("GET /accounts with session status = %d, body = %s", accounts.Code, accounts.Body.String())
	}

	// 登出后 cookie 失效
	logout := authedRequest(handler, cookie, http.MethodPost, "/auth/logout", nil)
	if logout.Code != http.StatusOK {
		t.Fatalf("POST /auth/logout status = %d", logout.Code)
	}
	after := authedRequest(handler, cookie, http.MethodGet, "/accounts", nil)
	if after.Code != http.StatusUnauthorized {
		t.Fatalf("GET /accounts after logout status = %d", after.Code)
	}
}

func TestAPITokenFlow(t *testing.T) {
	app, handler := newTestApp(t)
	cookie := loginForTest(t, handler, testAdminUser, testAdminPass)

	accA, err := app.db.UpsertAccount(context.Background(), "openid-a", "buffer-a", nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("create account A: %v", err)
	}
	accB, err := app.db.UpsertAccount(context.Background(), "openid-b", "buffer-b", nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("create account B: %v", err)
	}
	if accB.ID == accA.ID {
		t.Fatalf("account A and B share id %d", accA.ID)
	}

	// 令牌必须绑定账号：不带 ref 直接拒绝。
	noRef := authedRequest(handler, cookie, http.MethodPost, "/auth/token", nil)
	if noRef.Code != http.StatusBadRequest {
		t.Fatalf("POST /auth/token without ref status = %d, body = %s", noRef.Code, noRef.Body.String())
	}

	// 未生成时状态为 inactive。
	before := authedRequest(handler, cookie, http.MethodGet, "/auth/token?ref=openid-a", nil)
	if before.Code != http.StatusOK {
		t.Fatalf("GET /auth/token status = %d, body = %s", before.Code, before.Body.String())
	}
	var beforeBody struct {
		Data struct {
			Active bool `json:"active"`
		} `json:"data"`
	}
	if err := json.Unmarshal(before.Body.Bytes(), &beforeBody); err != nil {
		t.Fatalf("decode token status: %v", err)
	}
	if beforeBody.Data.Active {
		t.Fatalf("token should be inactive before generation")
	}

	tokenA := issueTokenForTest(t, handler, cookie, "openid-a")

	// Bearer Token 只返回绑定账号。
	list := bearerRequest(handler, tokenA, http.MethodGet, "/accounts", nil)
	if list.Code != http.StatusOK {
		t.Fatalf("GET /accounts with bearer token status = %d, body = %s", list.Code, list.Body.String())
	}
	var listBody struct {
		Data []struct {
			ID     int64  `json:"id"`
			OpenID string `json:"openid"`
		} `json:"data"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &listBody); err != nil {
		t.Fatalf("decode accounts list: %v", err)
	}
	if len(listBody.Data) != 1 || listBody.Data[0].ID != accA.ID {
		t.Fatalf("bearer token accounts = %#v, want only account A(id=%d)", listBody.Data, accA.ID)
	}

	// 操作其他账号被拒绝（403）。
	cross := bearerRequest(handler, tokenA, http.MethodPost, "/accounts/refresh", []byte(`{"ref":"openid-b"}`))
	if cross.Code != http.StatusForbidden {
		t.Fatalf("bearer token A operating account B status = %d, body = %s", cross.Code, cross.Body.String())
	}

	// 非白名单接口被拒绝（403）。
	forbidden := bearerRequest(handler, tokenA, http.MethodDelete, "/accounts?ref=openid-a", nil)
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("bearer token DELETE /accounts status = %d, body = %s", forbidden.Code, forbidden.Body.String())
	}

	// 为账号 B 生成令牌不影响账号 A 的令牌。
	tokenB := issueTokenForTest(t, handler, cookie, "openid-b")
	if tokenB == tokenA {
		t.Fatalf("account B token equals account A token")
	}
	stillOK := bearerRequest(handler, tokenA, http.MethodGet, "/accounts", nil)
	if stillOK.Code != http.StatusOK {
		t.Fatalf("token A status after issuing token B = %d", stillOK.Code)
	}

	// 同账号轮换令牌后旧令牌立即失效。
	rotatedA := issueTokenForTest(t, handler, cookie, "openid-a")
	if rotatedA == tokenA {
		t.Fatalf("rotated token equals old token")
	}
	revoked := bearerRequest(handler, tokenA, http.MethodGet, "/accounts", nil)
	if revoked.Code != http.StatusUnauthorized {
		t.Fatalf("GET /accounts with rotated-away token status = %d", revoked.Code)
	}

	// 显式吊销账号 B 的令牌。
	del := authedRequest(handler, cookie, http.MethodDelete, "/auth/token", []byte(`{"ref":"openid-b"}`))
	if del.Code != http.StatusOK {
		t.Fatalf("DELETE /auth/token status = %d, body = %s", del.Code, del.Body.String())
	}
	afterRevoke := bearerRequest(handler, tokenB, http.MethodGet, "/accounts", nil)
	if afterRevoke.Code != http.StatusUnauthorized {
		t.Fatalf("GET /accounts with revoked token B status = %d", afterRevoke.Code)
	}

	// 未知 ref 返回 404。
	unknown := authedRequest(handler, cookie, http.MethodPost, "/auth/token", []byte(`{"ref":"openid-missing"}`))
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("POST /auth/token with unknown ref status = %d", unknown.Code)
	}
}

func TestProtectedRoutesRequireAuth(t *testing.T) {
	_, handler := newTestApp(t)

	// 页面路径未登录跳转登录页
	page := authedRequest(handler, nil, http.MethodGet, "/", nil)
	if page.Code != http.StatusFound {
		t.Fatalf("GET / unauthenticated status = %d", page.Code)
	}
	if got := page.Header().Get("Location"); got != "/login" {
		t.Fatalf("GET / unauthenticated Location = %q", got)
	}
	scan := authedRequest(handler, nil, http.MethodGet, "/scan", nil)
	if scan.Code != http.StatusFound || scan.Header().Get("Location") != "/login" {
		t.Fatalf("GET /scan unauthenticated status = %d, location = %q", scan.Code, scan.Header().Get("Location"))
	}

	// API 路径未登录返回 401 envelope
	for _, path := range []string{"/accounts", "/openapi.json", "/wxapp/getCode"} {
		rec := authedRequest(handler, nil, http.MethodGet, path, nil)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("GET %s unauthenticated status = %d", path, rec.Code)
		}
		var body struct {
			Code int    `json:"code"`
			Msg  string `json:"msg"`
			Data any    `json:"data"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode %s 401 body: %v", path, err)
		}
		if body.Code != http.StatusUnauthorized || body.Msg == "" {
			t.Fatalf("GET %s 401 body = %#v", path, body)
		}
	}

	// 登录页与静态资源公开
	loginPage := authedRequest(handler, nil, http.MethodGet, "/login", nil)
	if loginPage.Code != http.StatusOK {
		t.Fatalf("GET /login unauthenticated status = %d", loginPage.Code)
	}
	static := authedRequest(handler, nil, http.MethodGet, "/static/css/base.css", nil)
	if static.Code != http.StatusOK {
		t.Fatalf("GET /static/css/base.css status = %d", static.Code)
	}
}

func TestChangePassword(t *testing.T) {
	_, handler := newTestApp(t)
	cookie := loginForTest(t, handler, testAdminUser, testAdminPass)

	// 新密码过短被拒
	short := authedRequest(handler, cookie, http.MethodPost, "/auth/password",
		[]byte(`{"old_password":"`+testAdminPass+`","new_password":"123"}`))
	if short.Code != http.StatusBadRequest {
		t.Fatalf("change to short password status = %d", short.Code)
	}

	// 旧密码错误被拒
	wrongOld := authedRequest(handler, cookie, http.MethodPost, "/auth/password",
		[]byte(`{"old_password":"wrong","new_password":"new-password-456"}`))
	if wrongOld.Code != http.StatusUnauthorized {
		t.Fatalf("change with wrong old password status = %d", wrongOld.Code)
	}

	// 正常改密
	ok := authedRequest(handler, cookie, http.MethodPost, "/auth/password",
		[]byte(`{"old_password":"`+testAdminPass+`","new_password":"new-password-456"}`))
	if ok.Code != http.StatusOK {
		t.Fatalf("change password status = %d, body = %s", ok.Code, ok.Body.String())
	}

	// 旧密码不能再登录
	oldLogin := authedRequest(handler, nil, http.MethodPost, "/auth/login",
		[]byte(`{"username":"admin","password":"`+testAdminPass+`"}`))
	if oldLogin.Code != http.StatusUnauthorized {
		t.Fatalf("login with old password after change status = %d", oldLogin.Code)
	}

	// 新密码可以登录
	loginForTest(t, handler, testAdminUser, "new-password-456")
}
