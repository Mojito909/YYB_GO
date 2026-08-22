package qr

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"sync"
	"time"

	"yyb_go/internal/protocol"
)

const (
	oauthURL = "https://open.weixin.qq.com/connect/qrconnect" +
		"?appid=wxd44977328b36e647" +
		"&redirect_uri=https://yybadaccess.3g.qq.com/pc_yyb/pcyyb_oauth?login_type=WX" +
		"&response_type=code&scope=snsapi_login,snsapi_runtime_pcsdk" +
		"&state=web&fast_login=1&self_redirect=true"
	callbackURL  = "https://yybadaccess.3g.qq.com/pc_yyb/pcyyb_oauth"
	qrBase       = "https://open.weixin.qq.com/connect/qrcode/"
	longPollBase = "https://long.open.weixin.qq.com/connect/l/qrconnect"

	// longPollTimeout 是单次微信长轮询的最长等待时间；微信侧通常在 25s 左右返回 408。
	longPollTimeout = 35 * time.Second
	// watchMinInterval 保证后台 watcher 即使遇到即时返回也不会形成忙循环。
	watchMinInterval = time.Second
	// watchRetryDelay 是长轮询出错（超时、网络抖动）后的重试间隔。
	watchRetryDelay = 2 * time.Second
)

var (
	uuidRE = regexp.MustCompile(`/connect/qrcode/([^"'>\s]+)`)
	errRE  = regexp.MustCompile(`wx_errcode\s*=\s*(\d+)`)
	codeRE = regexp.MustCompile(`wx_code\s*=\s*'([^']*)'`)
)

type Session struct {
	ID            string
	WXUUID        string
	QRCodeURL     string
	CreatedAt     time.Time
	HTTPClient    *http.Client
	Jar           http.CookieJar
	AuthorizeCode string
	Credentials   *protocol.LoginBufferCredentials
	LoginBuffer   string
	Status        string
	Error         string

	// pollClient 专用于微信长轮询，超时必须大于 longPollTimeout，
	// 否则会被 HTTPClient 的短超时（RequestTimeout）提前打断。
	pollClient *http.Client
	// errCode 保存最近一次微信返回的 wx_errcode，供状态快照透出。
	errCode *int
	// mu 只保护上面的状态字段，任何网络请求都不得在持有它时进行，
	// 这样 /qr/{id}/poll 才能始终毫秒级读到快照。
	mu sync.Mutex
	// confirmMu 串行化 confirm 流程，避免并发重复换取 login buffer。
	confirmMu sync.Mutex
	// stop 关闭后后台 watcher 退出；stopped 保证幂等且不会被重新启动。
	stop    chan struct{}
	stopped bool
}

func (s *Session) Age() time.Duration { return time.Since(s.CreatedAt) }

// Snapshot 无阻塞返回当前登录状态，由后台 watcher 持续刷新。
func (s *Session) Snapshot() PollResult {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.LoginBuffer != "" {
		return PollResult{Status: "confirmed"}
	}
	if s.AuthorizeCode != "" {
		code := 405
		return PollResult{Status: "authorized", ErrCode: &code, Code: s.AuthorizeCode}
	}
	status := s.Status
	if status == "" {
		status = "pending"
	}
	var errCode *int
	if s.errCode != nil {
		n := *s.errCode
		errCode = &n
	}
	return PollResult{Status: status, ErrCode: errCode, Message: s.Error}
}

// StopWatch 幂等地停止后台 watcher，并中断其在飞的长轮询。
func (s *Session) StopWatch() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stopped {
		return
	}
	s.stopped = true
	if s.stop != nil {
		close(s.stop)
	}
}

type ImageResult struct {
	Session    *Session
	ImageBytes []byte
}

type PollResult struct {
	Status  string `json:"status"`
	ErrCode *int   `json:"errcode,omitempty"`
	Code    string `json:"-"`
	Message string `json:"-"`
}

type Client struct {
	timeout      time.Duration
	loginBuffers *protocol.LoginBufferClient
}

func NewClient(timeout time.Duration) *Client {
	return &Client{
		timeout:      timeout,
		loginBuffers: protocol.NewLoginBufferClient(timeout),
	}
}

func (c *Client) LoginBuffers() *protocol.LoginBufferClient { return c.loginBuffers }

func (c *Client) GetQRCodeImage(ctx context.Context) (ImageResult, error) {
	sess, err := c.CreateSession(ctx)
	if err != nil {
		return ImageResult{}, err
	}
	data, err := c.FetchQRCodeImage(ctx, sess)
	if err != nil {
		return ImageResult{}, err
	}
	return ImageResult{Session: sess, ImageBytes: data}, nil
}

func (c *Client) CreateSession(ctx context.Context) (*Session, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	hc := &http.Client{Timeout: c.timeout, Jar: jar}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, oauthURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	m := uuidRE.FindStringSubmatch(string(body))
	if len(m) < 2 {
		return nil, fmt.Errorf("failed to parse WeChat QR uuid")
	}
	id, err := randomHex(16)
	if err != nil {
		return nil, err
	}
	wxuuid := m[1]
	return &Session{
		ID:         id,
		WXUUID:     wxuuid,
		QRCodeURL:  qrBase + wxuuid,
		CreatedAt:  time.Now(),
		HTTPClient: hc,
		Jar:        jar,
		Status:     "pending",
		pollClient: &http.Client{Timeout: longPollTimeout + 5*time.Second, Jar: jar},
		stop:       make(chan struct{}),
	}, nil
}

func (c *Client) FetchQRCodeImage(ctx context.Context, sess *Session) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sess.QRCodeURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := sess.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("QR image HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// StartWatch 启动后台 goroutine 持续代理微信长轮询并把结果写入会话状态。
// HTTP 侧因此只需读 Snapshot()，不再被 25~35s 的长轮询挂住。
func (c *Client) StartWatch(sess *Session) {
	sess.mu.Lock()
	if sess.stopped {
		sess.mu.Unlock()
		return
	}
	if sess.stop == nil {
		sess.stop = make(chan struct{})
	}
	if sess.pollClient == nil {
		sess.pollClient = &http.Client{Timeout: longPollTimeout + 5*time.Second, Jar: sess.Jar}
	}
	stop := sess.stop
	sess.mu.Unlock()
	go c.watch(sess, stop)
}

func (c *Client) watch(sess *Session, stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		default:
		}
		start := time.Now()
		status, err := c.pollOnce(sess, stop)
		if err != nil {
			// 长轮询超时或网络抖动：微信侧未给出状态，保持原状态稍后重试。
			select {
			case <-stop:
				return
			case <-time.After(watchRetryDelay):
			}
			continue
		}
		if terminal(status) {
			return
		}
		// 微信偶尔会立刻返回，加一个下限间隔避免空转。
		if wait := watchMinInterval - time.Since(start); wait > 0 {
			select {
			case <-stop:
				return
			case <-time.After(wait):
			}
		}
	}
}

// pollOnce 执行一次微信长轮询，并把解析结果写回会话状态，返回最新状态名。
// 注意：网络请求期间不持有 sess.mu。
func (c *Client) pollOnce(sess *Session, stop <-chan struct{}) (string, error) {
	u := longPollBase + "?" + url.Values{
		"uuid": {sess.WXUUID},
		"_":    {strconv.FormatInt(time.Now().UnixMilli(), 10)},
	}.Encode()
	ctx, cancel := context.WithTimeout(context.Background(), longPollTimeout)
	defer cancel()
	// 会话被丢弃时立即中断在飞的长轮询。
	go func() {
		select {
		case <-stop:
			cancel()
		case <-ctx.Done():
		}
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := sess.pollClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	text := string(body)
	errcode, code := parsePoll(text)

	sess.mu.Lock()
	defer sess.mu.Unlock()
	sess.errCode = errcode
	switch {
	case errcode != nil && *errcode == 408:
		sess.Status = "pending"
	case errcode != nil && *errcode == 404:
		sess.Status = "scanned"
	case errcode != nil && *errcode == 403:
		sess.Status = "cancelled"
	case errcode != nil && *errcode == 402:
		sess.Status = "expired"
	case errcode != nil && *errcode == 405 && code != "":
		sess.AuthorizeCode = code
		sess.Status = "authorized"
	default:
		if len(text) > 200 {
			text = text[:200]
		}
		sess.Status = "unknown"
		sess.Error = text
	}
	return sess.Status, nil
}

// terminal 判断某个状态是否已无需继续轮询。
func terminal(status string) bool {
	switch status {
	case "authorized", "confirmed", "cancelled", "expired", "unknown":
		return true
	}
	return false
}

func (c *Client) GetLoginBuffer(ctx context.Context, sess *Session) (protocol.LoginBufferResult, error) {
	// confirmMu 串行化换取流程；网络请求期间不持有 sess.mu，保证 Snapshot() 不被阻塞。
	sess.confirmMu.Lock()
	defer sess.confirmMu.Unlock()

	sess.mu.Lock()
	buffer, creds0, authCode := sess.LoginBuffer, sess.Credentials, sess.AuthorizeCode
	sess.mu.Unlock()
	if buffer != "" && creds0 != nil {
		return protocol.LoginBufferResult{LoginBuffer: buffer, Credentials: *creds0}, nil
	}
	if authCode == "" {
		return protocol.LoginBufferResult{}, fmt.Errorf("QR session is not authorized yet")
	}
	cb := callbackURL + "?" + url.Values{
		"login_type": {"WX"},
		"code":       {authCode},
		"state":      {"web"},
	}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cb, nil)
	if err != nil {
		return protocol.LoginBufferResult{}, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := sess.HTTPClient.Do(req)
	if err != nil {
		return protocol.LoginBufferResult{}, err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	creds, err := credentialsFromJar(sess.Jar, cb)
	if err != nil {
		return protocol.LoginBufferResult{}, err
	}
	lb, err := c.loginBuffers.FetchLoginBuffer(ctx, creds)
	if err != nil {
		return protocol.LoginBufferResult{}, err
	}
	sess.mu.Lock()
	sess.Credentials = &creds
	sess.LoginBuffer = lb
	sess.Status = "confirmed"
	sess.mu.Unlock()
	return protocol.LoginBufferResult{LoginBuffer: lb, Credentials: creds}, nil
}

func (c *Client) RefreshLoginBuffer(ctx context.Context, creds protocol.LoginBufferCredentials) (protocol.LoginBufferResult, error) {
	return c.loginBuffers.RefreshLoginBuffer(ctx, creds)
}

func DataURIJPEG(data []byte) string {
	return "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(data)
}

func parsePoll(text string) (*int, string) {
	var out *int
	if m := errRE.FindStringSubmatch(text); len(m) == 2 {
		n, _ := strconv.Atoi(m[1])
		out = &n
	}
	code := ""
	if m := codeRE.FindStringSubmatch(text); len(m) == 2 {
		code = m[1]
	}
	return out, code
}

func credentialsFromJar(jar http.CookieJar, rawURL string) (protocol.LoginBufferCredentials, error) {
	u, _ := url.Parse(rawURL)
	var cookies []*http.Cookie
	if u != nil {
		cookies = append(cookies, jar.Cookies(u)...)
	}
	hostURL, _ := url.Parse("https://yybadaccess.3g.qq.com/")
	cookies = append(cookies, jar.Cookies(hostURL)...)
	values := map[string]string{}
	for _, ck := range cookies {
		values[ck.Name] = ck.Value
	}
	openid := values["openid"]
	accessToken := values["accesstoken"]
	if openid == "" || accessToken == "" {
		return protocol.LoginBufferCredentials{}, fmt.Errorf("OAuth callback did not set required cookies")
	}
	expiresIn := int64(7200)
	if values["expires_in"] != "" {
		if n, err := strconv.ParseInt(values["expires_in"], 10, 64); err == nil && n > 0 {
			expiresIn = n
		}
	}
	return protocol.LoginBufferCredentials{
		OpenID:       openid,
		AccessToken:  accessToken,
		RefreshToken: values["refreshtoken"],
		LoginType:    defaultString(values["logintype"], "WX"),
		Nickname:     values["nickname"],
		ExpiresAt:    time.Now().Unix() + expiresIn,
		ExpiresIn:    expiresIn,
	}, nil
}

func defaultString(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
