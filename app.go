package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const appVersion = "1.0.0"

type App struct {
	store       *Store
	stockURL    string
	httpClient  *http.Client
	logger      *log.Logger
	trigger     chan struct{}
	checkMu     sync.Mutex
	startedAt   time.Time
	nextCheckAt atomic.Int64

	loginMu      sync.Mutex
	loginAttempt map[string]loginAttempt
}

type loginAttempt struct {
	Count   int
	ResetAt time.Time
}

type PublicSMTPConfig struct {
	Enabled       bool     `json:"enabled"`
	Host          string   `json:"host"`
	Port          int      `json:"port"`
	Security      string   `json:"security"`
	Username      string   `json:"username"`
	From          string   `json:"from"`
	Recipients    []string `json:"recipients"`
	SubjectPrefix string   `json:"subjectPrefix"`
	HasPassword   bool     `json:"hasPassword"`
}

type PublicSettings struct {
	PollIntervalSeconds int              `json:"pollIntervalSeconds"`
	SMTP                PublicSMTPConfig `json:"smtp"`
}

type AdminState struct {
	Version       string               `json:"version"`
	ServerTime    time.Time            `json:"serverTime"`
	StartedAt     time.Time            `json:"startedAt"`
	SourceURL     string               `json:"sourceUrl"`
	Summary       Summary              `json:"summary"`
	Products      []ProductView        `json:"products"`
	Settings      PublicSettings       `json:"settings"`
	APIToken      string               `json:"apiToken"`
	LastCheck     *CheckRecord         `json:"lastCheck"`
	NextCheckAt   *time.Time           `json:"nextCheckAt"`
	Notifications []NotificationRecord `json:"notifications"`
	StockEvents   []StockEvent         `json:"stockEvents"`
	Logs          []LogRecord          `json:"logs"`
}

type StockResponse struct {
	GeneratedAt time.Time     `json:"generatedAt"`
	LastCheck   *CheckRecord  `json:"lastCheck"`
	Summary     Summary       `json:"summary"`
	Products    []ProductView `json:"products"`
}

func NewApp(
	store *Store,
	stockURL string,
	logger *log.Logger,
	adminPassword string,
	resetAdminPassword string,
	apiToken string,
) (*App, string, error) {
	initialPassword, err := store.Bootstrap(adminPassword, apiToken)
	if err != nil {
		return nil, "", err
	}
	if resetAdminPassword != "" {
		if err := store.SetPassword(resetAdminPassword); err != nil {
			return nil, "", fmt.Errorf("reset admin password: %w", err)
		}
	}

	app := &App{
		store:    store,
		stockURL: stockURL,
		httpClient: &http.Client{
			Timeout: 20 * time.Second,
		},
		logger:       logger,
		trigger:      make(chan struct{}, 1),
		startedAt:    time.Now().UTC(),
		loginAttempt: make(map[string]loginAttempt),
	}
	_ = app.recordLog(context.Background(), "info", "服务已启动")
	return app, initialPassword, nil
}

func (a *App) RunScheduler(ctx context.Context) {
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		case <-a.trigger:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
		}

		a.runCheck(ctx)
		settings, err := a.store.GetSettings()
		interval := time.Duration(defaultPollSeconds) * time.Second
		if err == nil && settings.PollIntervalSeconds >= minPollSeconds {
			interval = time.Duration(settings.PollIntervalSeconds) * time.Second
		}
		a.nextCheckAt.Store(time.Now().Add(interval).UTC().UnixNano())
		timer.Reset(interval)
	}
}

func (a *App) TriggerCheck() {
	select {
	case a.trigger <- struct{}{}:
	default:
	}
}

func (a *App) runCheck(ctx context.Context) {
	a.checkMu.Lock()
	defer a.checkMu.Unlock()

	started := time.Now().UTC()
	stock, err := a.fetchStock(ctx)
	if err != nil {
		_, _ = a.store.RecordFailedCheck(ctx, started, err)
		_ = a.recordLog(ctx, "error", "库存检查失败: "+err.Error())
		return
	}

	outcome, err := a.store.ApplyStock(ctx, stock)
	if err != nil {
		_, _ = a.store.RecordFailedCheck(ctx, started, err)
		_ = a.recordLog(ctx, "error", "保存库存失败: "+err.Error())
		return
	}

	message := fmt.Sprintf(
		"库存检查完成: %d 个套餐，%d 个有货",
		outcome.Record.TotalProducts,
		outcome.Record.Available,
	)
	if len(outcome.NewStockIDs) > 0 {
		message += fmt.Sprintf("，新增补货 %d 个", len(outcome.NewStockIDs))
	}
	_ = a.recordLog(ctx, "info", message)

	if len(outcome.PendingAlertIDs) == 0 {
		return
	}

	settings, err := a.store.GetSettings()
	if err != nil {
		_ = a.recordLog(ctx, "error", "读取邮件配置失败: "+err.Error())
		return
	}
	if !settings.SMTP.Enabled {
		_ = a.recordLog(ctx, "warn", "检测到待通知补货，但 SMTP 邮件通知尚未启用")
		return
	}

	products, err := a.store.ListProducts(ctx)
	if err != nil {
		_ = a.recordLog(ctx, "error", "读取补货套餐失败: "+err.Error())
		return
	}
	pendingSet := make(map[string]struct{}, len(outcome.PendingAlertIDs))
	for _, id := range outcome.PendingAlertIDs {
		pendingSet[id] = struct{}{}
	}
	items := make([]ProductView, 0, len(pendingSet))
	for _, product := range products {
		if _, ok := pendingSet[product.ID]; ok {
			items = append(items, product)
		}
	}
	if len(items) == 0 {
		return
	}

	subject := fmt.Sprintf("%s 补货提醒: %d 个套餐有货", settings.SMTP.SubjectPrefix, len(items))
	body := buildStockEmail(items)
	sendErr := sendEmail(settings.SMTP, subject, body)
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}
	if sendErr != nil {
		_ = a.store.RecordNotification(ctx, "failed", subject, settings.SMTP.Recipients, ids, sendErr)
		_ = a.recordLog(ctx, "error", "补货邮件发送失败: "+sendErr.Error())
		return
	}
	if err := a.store.MarkAlertsSent(ctx, ids); err != nil {
		_ = a.recordLog(ctx, "error", "更新邮件状态失败: "+err.Error())
	}
	_ = a.store.RecordNotification(ctx, "sent", subject, settings.SMTP.Recipients, ids, nil)
	_ = a.recordLog(ctx, "info", fmt.Sprintf("补货邮件已发送，共 %d 个套餐", len(items)))
}

func (a *App) fetchStock(ctx context.Context) (map[string]int, error) {
	parsed, err := url.Parse(a.stockURL)
	if err != nil {
		return nil, fmt.Errorf("库存地址无效: %w", err)
	}
	query := parsed.Query()
	query.Set("_", fmt.Sprintf("%d", time.Now().UnixMilli()))
	parsed.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("User-Agent", "ShockStockMonitor/"+appVersion)

	response, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求库存接口失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return nil, fmt.Errorf("库存接口返回 HTTP %d", response.StatusCode)
	}

	var stock map[string]int
	decoder := json.NewDecoder(io.LimitReader(response.Body, 2<<20))
	if err := decoder.Decode(&stock); err != nil {
		return nil, fmt.Errorf("解析库存接口失败: %w", err)
	}
	if len(stock) == 0 {
		return nil, errors.New("库存接口返回空数据")
	}
	for id, value := range stock {
		if value < 0 || value > 100000 {
			return nil, fmt.Errorf("库存数据异常: product=%s value=%d", id, value)
		}
	}
	return stock, nil
}

func (a *App) AdminState(ctx context.Context) (AdminState, error) {
	settings, err := a.store.GetSettings()
	if err != nil {
		return AdminState{}, err
	}
	products, err := a.store.ListProducts(ctx)
	if err != nil {
		return AdminState{}, err
	}
	lastCheck, err := a.store.LatestCheck(ctx)
	if err != nil {
		return AdminState{}, err
	}
	notifications, err := a.store.ListNotifications(ctx, 20)
	if err != nil {
		return AdminState{}, err
	}
	events, err := a.store.ListStockEvents(ctx, 30)
	if err != nil {
		return AdminState{}, err
	}
	logs, err := a.store.ListLogs(ctx, 80)
	if err != nil {
		return AdminState{}, err
	}
	apiToken, err := a.store.APIToken()
	if err != nil {
		return AdminState{}, err
	}

	return AdminState{
		Version:       appVersion,
		ServerTime:    time.Now().UTC(),
		StartedAt:     a.startedAt,
		SourceURL:     a.stockURL,
		Summary:       summarize(products),
		Products:      products,
		Settings:      publicSettings(settings),
		APIToken:      apiToken,
		LastCheck:     lastCheck,
		NextCheckAt:   a.nextCheckTime(),
		Notifications: notifications,
		StockEvents:   events,
		Logs:          logs,
	}, nil
}

func (a *App) StockState(ctx context.Context) (StockResponse, error) {
	products, err := a.store.ListProducts(ctx)
	if err != nil {
		return StockResponse{}, err
	}
	lastCheck, err := a.store.LatestCheck(ctx)
	if err != nil {
		return StockResponse{}, err
	}
	return StockResponse{
		GeneratedAt: time.Now().UTC(),
		LastCheck:   lastCheck,
		Summary:     summarize(products),
		Products:    products,
	}, nil
}

func (a *App) SendTestEmail(ctx context.Context) error {
	settings, err := a.store.GetSettings()
	if err != nil {
		return err
	}
	if err := validateSMTP(settings.SMTP); err != nil {
		return err
	}
	subject := settings.SMTP.SubjectPrefix + " SMTP 测试邮件"
	body := "这是一封来自 Shock Hosting 补货监控服务的 SMTP 测试邮件。\n\n如果你收到这封邮件，说明邮件配置可以正常使用。"
	sendErr := sendEmail(settings.SMTP, subject, body)
	productIDs := []string{}
	if sendErr != nil {
		_ = a.store.RecordNotification(ctx, "failed", subject, settings.SMTP.Recipients, productIDs, sendErr)
		_ = a.recordLog(ctx, "error", "SMTP 测试失败: "+sendErr.Error())
		return sendErr
	}
	_ = a.store.RecordNotification(ctx, "test", subject, settings.SMTP.Recipients, productIDs, nil)
	_ = a.recordLog(ctx, "info", "SMTP 测试邮件已发送")
	return nil
}

func (a *App) LoginAllowed(ip string) bool {
	a.loginMu.Lock()
	defer a.loginMu.Unlock()
	attempt, exists := a.loginAttempt[ip]
	now := time.Now()
	if !exists || now.After(attempt.ResetAt) {
		return true
	}
	return attempt.Count < 6
}

func (a *App) RecordLoginFailure(ip string) {
	a.loginMu.Lock()
	defer a.loginMu.Unlock()
	now := time.Now()
	attempt := a.loginAttempt[ip]
	if now.After(attempt.ResetAt) {
		attempt = loginAttempt{ResetAt: now.Add(10 * time.Minute)}
	}
	attempt.Count++
	a.loginAttempt[ip] = attempt
}

func (a *App) ClearLoginFailures(ip string) {
	a.loginMu.Lock()
	defer a.loginMu.Unlock()
	delete(a.loginAttempt, ip)
}

func (a *App) nextCheckTime() *time.Time {
	value := a.nextCheckAt.Load()
	if value == 0 {
		return nil
	}
	next := time.Unix(0, value).UTC()
	return &next
}

func (a *App) recordLog(ctx context.Context, level, message string) error {
	prefix := strings.ToUpper(level)
	a.logger.Printf("%s %s", prefix, message)
	return a.store.RecordLog(ctx, level, message)
}

func publicSettings(settings Settings) PublicSettings {
	return PublicSettings{
		PollIntervalSeconds: settings.PollIntervalSeconds,
		SMTP: PublicSMTPConfig{
			Enabled:       settings.SMTP.Enabled,
			Host:          settings.SMTP.Host,
			Port:          settings.SMTP.Port,
			Security:      settings.SMTP.Security,
			Username:      settings.SMTP.Username,
			From:          settings.SMTP.From,
			Recipients:    append([]string(nil), settings.SMTP.Recipients...),
			SubjectPrefix: settings.SMTP.SubjectPrefix,
			HasPassword:   settings.SMTP.Password != "",
		},
	}
}

func summarize(products []ProductView) Summary {
	summary := Summary{Total: len(products)}
	for _, product := range products {
		if product.Watched {
			summary.Watched++
		}
		switch {
		case !product.Checked:
			summary.Unknown++
		case product.Stock > 0:
			summary.Available++
			if product.Watched {
				summary.WatchedLive++
			}
		default:
			summary.OutOfStock++
		}
	}
	return summary
}

func buildStockEmail(products []ProductView) string {
	var builder strings.Builder
	builder.WriteString("检测到 Shock Hosting 专用服务器补货。\n\n")
	for _, product := range products {
		builder.WriteString(fmt.Sprintf(
			"%s · %s\n%d GB RAM · %s · $%.2f/月\n当前库存: %d\n下单: %s\n\n",
			product.Name,
			product.LocationName,
			product.RAMGB,
			product.Storage,
			product.PriceMonthly,
			product.Stock,
			product.OrderURL,
		))
	}
	builder.WriteString("Web 管理: /admin\n")
	return builder.String()
}
