package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"time"
)

const sessionCookie = "shockstock_session"

type Handler struct {
	app      *App
	webFiles fs.FS
}

func NewHandler(app *App, webFiles fs.FS) http.Handler {
	handler := &Handler{app: app, webFiles: webFiles}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", handler.health)
	mux.HandleFunc("GET /api/v1/status", handler.apiToken(handler.apiStatus))
	mux.HandleFunc("GET /api/v1/stock", handler.apiToken(handler.apiStock))
	mux.HandleFunc("GET /api/v1/stock/{id}", handler.apiToken(handler.apiProduct))
	mux.HandleFunc("POST /api/v1/check", handler.apiToken(handler.apiCheck))

	mux.HandleFunc("POST /admin/api/login", handler.login)
	mux.HandleFunc("POST /admin/api/logout", handler.admin(handler.logout))
	mux.HandleFunc("GET /admin/api/state", handler.admin(handler.adminState))
	mux.HandleFunc("PUT /admin/api/settings", handler.admin(handler.saveSettings))
	mux.HandleFunc("POST /admin/api/check", handler.admin(handler.triggerCheck))
	mux.HandleFunc("POST /admin/api/test-email", handler.admin(handler.testEmail))
	mux.HandleFunc("PUT /admin/api/watch", handler.admin(handler.updateWatch))
	mux.HandleFunc("POST /admin/api/token/rotate", handler.admin(handler.rotateToken))
	mux.HandleFunc("PUT /admin/api/password", handler.admin(handler.changePassword))

	mux.HandleFunc("GET /admin", handler.serveIndex)
	mux.HandleFunc("GET /", handler.serveStatic)

	return securityHeaders(mux)
}

func (h *Handler) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"version": appVersion,
		"time":    time.Now().UTC(),
	})
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}
	ip := remoteIP(r)
	if !h.app.LoginAllowed(ip) {
		writeError(w, http.StatusTooManyRequests, "登录尝试过多，请稍后再试")
		return
	}
	var request struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式无效")
		return
	}
	ok, err := h.app.store.VerifyPassword(request.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "验证密码失败")
		return
	}
	if !ok {
		h.app.RecordLoginFailure(ip)
		writeError(w, http.StatusUnauthorized, "密码不正确")
		return
	}
	h.app.ClearLoginFailures(ip)
	token, err := h.app.store.CreateSession()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "创建登录会话失败")
		return
	}
	setSessionCookie(w, r, token, 24*time.Hour)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}
	cookie, _ := r.Cookie(sessionCookie)
	if cookie != nil {
		h.app.store.DeleteSession(cookie.Value)
	}
	setSessionCookie(w, r, "", -time.Hour)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Handler) adminState(w http.ResponseWriter, r *http.Request) {
	state, err := h.app.AdminState(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取管理数据失败")
		return
	}
	writeJSON(w, http.StatusOK, state)
}

type settingsRequest struct {
	PollIntervalSeconds int `json:"pollIntervalSeconds"`
	SMTP                struct {
		Enabled       bool     `json:"enabled"`
		Host          string   `json:"host"`
		Port          int      `json:"port"`
		Security      string   `json:"security"`
		Username      string   `json:"username"`
		Password      string   `json:"password"`
		From          string   `json:"from"`
		Recipients    []string `json:"recipients"`
		SubjectPrefix string   `json:"subjectPrefix"`
	} `json:"smtp"`
}

func (h *Handler) saveSettings(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}
	var request settingsRequest
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "配置格式无效")
		return
	}
	if request.PollIntervalSeconds < minPollSeconds || request.PollIntervalSeconds > maxPollSeconds {
		writeError(w, http.StatusBadRequest, fmt.Sprintf(
			"轮询间隔必须在 %d 到 %d 秒之间",
			minPollSeconds,
			maxPollSeconds,
		))
		return
	}

	current, err := h.app.store.GetSettings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取现有配置失败")
		return
	}
	smtp := SMTPConfig{
		Enabled:       request.SMTP.Enabled,
		Host:          strings.TrimSpace(request.SMTP.Host),
		Port:          request.SMTP.Port,
		Security:      strings.ToLower(strings.TrimSpace(request.SMTP.Security)),
		Username:      strings.TrimSpace(request.SMTP.Username),
		Password:      current.SMTP.Password,
		From:          strings.TrimSpace(request.SMTP.From),
		Recipients:    normalizeRecipients(request.SMTP.Recipients),
		SubjectPrefix: strings.TrimSpace(request.SMTP.SubjectPrefix),
	}
	if smtp.Port == 0 {
		smtp.Port = 587
	}
	if smtp.Security == "" {
		smtp.Security = "starttls"
	}
	if smtp.SubjectPrefix == "" {
		smtp.SubjectPrefix = "[Shock Stock]"
	}
	if request.SMTP.Password != "" {
		smtp.Password = request.SMTP.Password
	}
	if smtp.Enabled {
		if err := validateSMTP(smtp); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	settings := Settings{
		PollIntervalSeconds: request.PollIntervalSeconds,
		SMTP:                smtp,
	}
	if err := h.app.store.SaveSettings(settings); err != nil {
		writeError(w, http.StatusInternalServerError, "保存配置失败")
		return
	}
	_ = h.app.recordLog(r.Context(), "info", "监控与邮件配置已更新")
	h.app.TriggerCheck()
	writeJSON(w, http.StatusOK, publicSettings(settings))
}

func (h *Handler) triggerCheck(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}
	h.app.TriggerCheck()
	writeJSON(w, http.StatusAccepted, map[string]bool{"queued": true})
}

func (h *Handler) testEmail(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}
	if err := h.app.SendTestEmail(r.Context()); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"sent": true})
}

func (h *Handler) updateWatch(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}
	var request struct {
		IDs     []string `json:"ids"`
		Watched bool     `json:"watched"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式无效")
		return
	}
	if len(request.IDs) > len(catalog) {
		writeError(w, http.StatusBadRequest, "监控项数量无效")
		return
	}
	updated, err := h.app.store.UpdateWatch(r.Context(), request.IDs, request.Watched)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "更新监控项失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"updated": updated})
}

func (h *Handler) rotateToken(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}
	token, err := h.app.store.RotateAPIToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "更新 API Token 失败")
		return
	}
	_ = h.app.recordLog(r.Context(), "warn", "API Token 已重新生成")
	writeJSON(w, http.StatusOK, map[string]string{"token": token})
}

func (h *Handler) changePassword(w http.ResponseWriter, r *http.Request) {
	if !requireJSON(w, r) {
		return
	}
	var request struct {
		CurrentPassword string `json:"currentPassword"`
		NewPassword     string `json:"newPassword"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式无效")
		return
	}
	if len(request.NewPassword) < 10 {
		writeError(w, http.StatusBadRequest, "新密码至少需要 10 个字符")
		return
	}
	ok, err := h.app.store.VerifyPassword(request.CurrentPassword)
	if err != nil || !ok {
		writeError(w, http.StatusUnauthorized, "当前密码不正确")
		return
	}
	if err := h.app.store.SetPassword(request.NewPassword); err != nil {
		writeError(w, http.StatusInternalServerError, "更新密码失败")
		return
	}
	token, err := h.app.store.CreateSession()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "创建登录会话失败")
		return
	}
	setSessionCookie(w, r, token, 24*time.Hour)
	_ = h.app.recordLog(r.Context(), "warn", "管理密码已更新")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (h *Handler) apiStatus(w http.ResponseWriter, r *http.Request) {
	state, err := h.app.StockState(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取状态失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":          state.LastCheck != nil && state.LastCheck.OK,
		"version":     appVersion,
		"serverTime":  state.GeneratedAt,
		"lastCheck":   state.LastCheck,
		"summary":     state.Summary,
		"nextCheckAt": h.app.nextCheckTime(),
	})
}

func (h *Handler) apiStock(w http.ResponseWriter, r *http.Request) {
	state, err := h.app.StockState(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取库存失败")
		return
	}
	location := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("location")))
	status := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))
	filtered := make([]ProductView, 0, len(state.Products))
	for _, product := range state.Products {
		if location != "" && product.LocationCode != location {
			continue
		}
		switch status {
		case "", "all":
		case "available":
			if product.Stock <= 0 {
				continue
			}
		case "out":
			if !product.Checked || product.Stock > 0 {
				continue
			}
		case "unknown":
			if product.Checked {
				continue
			}
		case "watched":
			if !product.Watched {
				continue
			}
		default:
			writeError(w, http.StatusBadRequest, "status 参数无效")
			return
		}
		filtered = append(filtered, product)
	}
	state.Products = filtered
	writeJSON(w, http.StatusOK, state)
}

func (h *Handler) apiProduct(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	product, exists := catalogByID[id]
	if !exists {
		writeError(w, http.StatusNotFound, "套餐不存在")
		return
	}
	products, err := h.app.store.ListProducts(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "读取库存失败")
		return
	}
	for _, view := range products {
		if view.ID == product.ID {
			writeJSON(w, http.StatusOK, view)
			return
		}
	}
	writeError(w, http.StatusNotFound, "套餐不存在")
}

func (h *Handler) apiCheck(w http.ResponseWriter, r *http.Request) {
	h.app.TriggerCheck()
	writeJSON(w, http.StatusAccepted, map[string]bool{"queued": true})
}

func (h *Handler) serveIndex(w http.ResponseWriter, r *http.Request) {
	data, err := fs.ReadFile(h.webFiles, "index.html")
	if err != nil {
		http.Error(w, "web application unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(data)
}

func (h *Handler) serveStatic(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" {
		h.serveIndex(w, r)
		return
	}
	cleanPath := strings.TrimPrefix(r.URL.Path, "/")
	if cleanPath == "" || strings.Contains(cleanPath, "..") {
		http.NotFound(w, r)
		return
	}
	data, err := fs.ReadFile(h.webFiles, cleanPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	switch {
	case strings.HasSuffix(cleanPath, ".css"):
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
	case strings.HasSuffix(cleanPath, ".js"):
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	case strings.HasSuffix(cleanPath, ".svg"):
		w.Header().Set("Content-Type", "image/svg+xml")
	}
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(data)
}

func (h *Handler) admin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookie)
		if err != nil || !h.app.store.ValidSession(cookie.Value) {
			writeError(w, http.StatusUnauthorized, "请先登录")
			return
		}
		next(w, r)
	}
}

func (h *Handler) apiToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, err := h.app.store.APIToken()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "读取 API Token 失败")
			return
		}
		presented := ""
		auth := strings.TrimSpace(r.Header.Get("Authorization"))
		if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
			presented = strings.TrimSpace(auth[7:])
		}
		if presented == "" {
			presented = strings.TrimSpace(r.Header.Get("X-API-Key"))
		}
		if presented == "" || !constantTimeStringEqual(token, presented) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="Shock Stock API"`)
			writeError(w, http.StatusUnauthorized, "API Token 无效")
			return
		}
		next(w, r)
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set(
			"Content-Security-Policy",
			"default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'",
		)
		next.ServeHTTP(w, r)
	})
}

func requireJSON(w http.ResponseWriter, r *http.Request) bool {
	contentType := strings.ToLower(r.Header.Get("Content-Type"))
	if !strings.HasPrefix(contentType, "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "Content-Type 必须是 application/json")
		return false
	}
	return true
}

func decodeJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("request body must contain one JSON value")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	payload, err := json.Marshal(value)
	if err != nil {
		http.Error(w, `{"error":"encoding response failed"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(append(payload, '\n'))
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, token string, maxAge time.Duration) {
	secure := r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	cookie := &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(maxAge.Seconds()),
	}
	http.SetCookie(w, cookie)
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}
