package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"yyb_go/internal/store"
)

const (
	apiLogBatchSize    = 64
	apiLogChannelSize  = 512
	apiLogFlushPeriod  = time.Minute
	apiLogWriteTimeout = 5 * time.Second
)

// apiLogWriter 把接口日志异步批量写入 SQLite，避免请求路径上同步写库，
// 同时把保留期清理从“每次调用”降为定时执行。
type apiLogWriter struct {
	db        *store.DB
	ch        chan store.APICallLog
	done      chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup
}

func newAPILogWriter(db *store.DB) *apiLogWriter {
	w := &apiLogWriter{
		db:   db,
		ch:   make(chan store.APICallLog, apiLogChannelSize),
		done: make(chan struct{}),
	}
	w.wg.Add(1)
	go w.loop()
	return w
}

func (w *apiLogWriter) enqueue(entry store.APICallLog) {
	select {
	case w.ch <- entry:
	default:
		// 队列已满时丢弃日志，避免反压阻塞业务请求。
	}
}

func (w *apiLogWriter) loop() {
	defer w.wg.Done()
	ticker := time.NewTicker(apiLogFlushPeriod)
	defer ticker.Stop()

	batch := make([]store.APICallLog, 0, apiLogBatchSize)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), apiLogWriteTimeout)
		if err := w.db.InsertAPICallLogs(ctx, batch); err != nil {
			log.Printf("[api-log] 写入失败: %v", err)
		}
		cancel()
		batch = batch[:0]
	}

	for {
		select {
		case entry := <-w.ch:
			batch = append(batch, entry)
			if len(batch) >= apiLogBatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
			ctx, cancel := context.WithTimeout(context.Background(), apiLogWriteTimeout)
			if _, err := w.db.PruneAPICallLogs(ctx); err != nil {
				log.Printf("[api-log] 清理过期日志失败: %v", err)
			}
			cancel()
		case <-w.done:
			for {
				select {
				case entry := <-w.ch:
					batch = append(batch, entry)
				default:
					flush()
					return
				}
			}
		}
	}
}

func (w *apiLogWriter) Close() {
	w.closeOnce.Do(func() { close(w.done) })
	w.wg.Wait()
}

// apiAuditMiddleware records calls made through the public wxapp API. It runs
// after authentication, so account-scoped bearer tokens are attributed to the
// account they belong to without persisting the token itself.
func (a *App) apiAuditMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !strings.HasPrefix(c.Request.URL.Path, "/wxapp/") {
			c.Next()
			return
		}
		started := time.Now()
		request := c.Request
		accountRef, _ := requestRef(request)
		appID := requestAppID(request)
		accountID := int64(0)
		if p, ok := principalFrom(request.Context()); ok && p.viaToken {
			accountID = p.accountID
		} else if accountRef != "" {
			if account, err := a.db.ResolveAccount(request.Context(), accountRef); err == nil {
				accountID = account.ID
			}
		}
		aw := &auditResponseWriter{ResponseWriter: c.Writer}
		c.Writer = aw
		c.Next()
		status := aw.status
		if status == 0 {
			status = http.StatusOK
		}
		if len(appID) > 240 {
			appID = appID[:240]
		}
		if len(accountRef) > 240 {
			accountRef = accountRef[:240]
		}
		entry := store.APICallLog{
			Endpoint:   request.URL.Path,
			Method:     request.Method,
			ClientIP:   requestClientIP(request, a.cfg.TrustProxy),
			AppID:      appID,
			StatusCode: status,
			Success:    status < 400,
			DurationMS: time.Since(started).Milliseconds(),
			CreatedAt:  time.Now().Unix(),
		}
		if accountRef != "" {
			entry.AccountRef = accountRef
		}
		if accountID != 0 {
			entry.AccountID = &accountID
		}
		if a.apiLogs != nil {
			a.apiLogs.enqueue(entry)
		}
	}
}

func requestAppID(r *http.Request) string {
	if r.Body == nil {
		return ""
	}
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return ""
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	var body struct {
		AppID string `json:"app_id"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return ""
	}
	return strings.TrimSpace(body.AppID)
}

type auditResponseWriter struct {
	gin.ResponseWriter
	status int
}

func (w *auditResponseWriter) WriteHeader(code int) {
	if w.status != 0 {
		return
	}
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *auditResponseWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}

// requestClientIP 返回请求来源 IP。只有显式开启 trustProxy 时才信任
// X-Forwarded-For / X-Real-IP，否则一律以 TCP 对端地址为准，避免伪造。
func requestClientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		for _, header := range []string{"X-Forwarded-For", "X-Real-IP"} {
			if value := strings.TrimSpace(r.Header.Get(header)); value != "" {
				if comma := strings.IndexByte(value, ','); comma >= 0 {
					value = value[:comma]
				}
				return strings.TrimSpace(value)
			}
		}
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err == nil {
		return host
	}
	return strings.TrimSpace(r.RemoteAddr)
}

func (a *App) handleAPILogs(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		filter := store.APICallLogFilter{
			Keyword: strings.TrimSpace(r.URL.Query().Get("q")),
			IP:      strings.TrimSpace(r.URL.Query().Get("ip")),
			Path:    strings.TrimSpace(r.URL.Query().Get("path")),
			Status:  strings.TrimSpace(r.URL.Query().Get("status")),
			Limit:   normalizeLogLimit(queryInt(r, "limit", 20)),
			Offset:  queryInt(r, "offset", 0),
			Since:   queryInt64(r, "since", 0),
			Until:   queryInt64(r, "until", 0),
		}
		items, total, err := a.db.ListAPICallLogs(r.Context(), filter)
		if err != nil {
			writeInternalError(w, err)
			return
		}
		settings, err := a.db.GetAPILogSettings(r.Context())
		if err != nil {
			writeInternalError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"items": items, "total": total, "limit": filter.Limit, "offset": filter.Offset,
			"retention_days": settings.RetentionDays,
		})
	case http.MethodDelete:
		count, err := a.db.ClearAPICallLogs(r.Context())
		if err != nil {
			writeInternalError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"deleted": count})
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func normalizeLogLimit(value int) int {
	switch value {
	case 20, 50, 100:
		return value
	default:
		return 20
	}
}

func (a *App) handleDashboardMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	period := strings.TrimSpace(r.URL.Query().Get("period"))
	if period == "" {
		period = "day"
	}
	metrics, err := a.db.GetAPIMetrics(r.Context(), period, time.Now())
	if err != nil {
		writeInternalError(w, err)
		return
	}
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	writeJSON(w, http.StatusOK, map[string]any{
		"metrics": metrics,
		"runtime": map[string]any{
			"goroutines":       runtime.NumGoroutine(),
			"heap_alloc_bytes": mem.HeapAlloc,
			"heap_alloc_mb":    float64(mem.HeapAlloc) / 1024 / 1024,
			"num_gc":           mem.NumGC,
			"uptime_seconds":   int64(time.Since(a.startedAt).Seconds()),
		},
	})
}

func (a *App) handleAPILogByID(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	idText := strings.TrimPrefix(r.URL.Path, "/api-logs/")
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid log id")
		return
	}
	if err := a.db.DeleteAPICallLog(r.Context(), id); err != nil {
		writeInternalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id})
}

func (a *App) handleAPILogSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		settings, err := a.db.GetAPILogSettings(r.Context())
		if err != nil {
			writeInternalError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, settings)
	case http.MethodPatch, http.MethodPost:
		var body struct {
			RetentionDays int `json:"retention_days"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
			return
		}
		settings, err := a.db.SetAPILogRetention(r.Context(), body.RetentionDays)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, settings)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func queryInt(r *http.Request, key string, fallback int) int {
	value, err := strconv.Atoi(r.URL.Query().Get(key))
	if err != nil {
		return fallback
	}
	return value
}

func queryInt64(r *http.Request, key string, fallback int64) int64 {
	value, err := strconv.ParseInt(r.URL.Query().Get(key), 10, 64)
	if err != nil {
		return fallback
	}
	return value
}
