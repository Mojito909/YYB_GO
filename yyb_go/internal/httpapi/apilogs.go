package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"yyb_go/internal/store"
)

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
		log := store.APICallLog{
			Endpoint:   request.URL.Path,
			Method:     request.Method,
			ClientIP:   requestClientIP(request),
			AppID:      appID,
			StatusCode: status,
			Success:    status < 400,
			DurationMS: time.Since(started).Milliseconds(),
			CreatedAt:  time.Now().Unix(),
		}
		if accountRef != "" {
			log.AccountRef = accountRef
		}
		if accountID != 0 {
			log.AccountID = &accountID
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = a.db.RecordAPICall(ctx, log)
		cancel()
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

func requestClientIP(r *http.Request) string {
	for _, header := range []string{"X-Forwarded-For", "X-Real-IP"} {
		if value := strings.TrimSpace(r.Header.Get(header)); value != "" {
			if comma := strings.IndexByte(value, ','); comma >= 0 {
				value = value[:comma]
			}
			return strings.TrimSpace(value)
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
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		settings, err := a.db.GetAPILogSettings(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"items": items, "total": total, "limit": filter.Limit, "offset": filter.Offset,
			"retention_days": settings.RetentionDays,
		})
	case http.MethodDelete:
		count, err := a.db.ClearAPICallLogs(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
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
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	writeJSON(w, http.StatusOK, map[string]any{
		"metrics": metrics,
		"runtime": map[string]any{
			"goroutines": runtime.NumGoroutine(),
			"heap_alloc_bytes": mem.HeapAlloc,
			"heap_alloc_mb": float64(mem.HeapAlloc) / 1024 / 1024,
			"num_gc": mem.NumGC,
			"uptime_seconds": int64(time.Since(a.startedAt).Seconds()),
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
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id})
}

func (a *App) handleAPILogSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		settings, err := a.db.GetAPILogSettings(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
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
