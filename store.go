package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const (
	defaultPollSeconds = 60
	minPollSeconds     = 30
	maxPollSeconds     = 86400
	maxAlertsPerEvent  = 3
)

type SMTPConfig struct {
	Enabled       bool     `json:"enabled"`
	Host          string   `json:"host"`
	Port          int      `json:"port"`
	Security      string   `json:"security"`
	Username      string   `json:"username"`
	Password      string   `json:"password,omitempty"`
	From          string   `json:"from"`
	Recipients    []string `json:"recipients"`
	SubjectPrefix string   `json:"subjectPrefix"`
}

type Settings struct {
	PollIntervalSeconds int
	SMTP                SMTPConfig
}

type ProductView struct {
	Product
	Watched       bool       `json:"watched"`
	Stock         int        `json:"stock"`
	PreviousStock int        `json:"previousStock"`
	Checked       bool       `json:"checked"`
	AlertPending  bool       `json:"alertPending"`
	AlertCount    int        `json:"alertCount"`
	LastAlertAt   *time.Time `json:"lastAlertAt"`
	NextAlertAt   *time.Time `json:"nextAlertAt"`
	LastSeenAt    *time.Time `json:"lastSeenAt"`
	LastChangedAt *time.Time `json:"lastChangedAt"`
}

type Summary struct {
	Total       int `json:"total"`
	Available   int `json:"available"`
	OutOfStock  int `json:"outOfStock"`
	Unknown     int `json:"unknown"`
	Watched     int `json:"watched"`
	WatchedLive int `json:"watchedLive"`
}

type CheckRecord struct {
	ID            int64     `json:"id"`
	StartedAt     time.Time `json:"startedAt"`
	FinishedAt    time.Time `json:"finishedAt"`
	OK            bool      `json:"ok"`
	Error         string    `json:"error,omitempty"`
	Available     int       `json:"available"`
	TotalProducts int       `json:"totalProducts"`
	NewStock      int       `json:"newStock"`
}

type StockEvent struct {
	ID        int64     `json:"id"`
	ProductID string    `json:"productId"`
	Product   string    `json:"product"`
	OldStock  int       `json:"oldStock"`
	NewStock  int       `json:"newStock"`
	CreatedAt time.Time `json:"createdAt"`
}

type NotificationRecord struct {
	ID         int64     `json:"id"`
	Status     string    `json:"status"`
	Subject    string    `json:"subject"`
	Recipients string    `json:"recipients"`
	ProductIDs string    `json:"productIds"`
	Error      string    `json:"error,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
}

type LogRecord struct {
	ID        int64     `json:"id"`
	Level     string    `json:"level"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"createdAt"`
}

type CheckOutcome struct {
	Record          CheckRecord
	NewStockIDs     []string
	PendingAlertIDs []string
}

type Store struct {
	db *sql.DB
}

func OpenStore(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	path := filepath.Join(dataDir, "shockstock.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	store := &Store{db: db}
	if err := store.initialize(); err != nil {
		db.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) initialize() error {
	pragmas := []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = NORMAL",
		"PRAGMA foreign_keys = ON",
		"PRAGMA busy_timeout = 5000",
	}
	for _, pragma := range pragmas {
		if _, err := s.db.Exec(pragma); err != nil {
			return fmt.Errorf("%s: %w", pragma, err)
		}
	}

	schema := `
CREATE TABLE IF NOT EXISTS settings (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS products (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL,
	location_code TEXT NOT NULL,
	location_name TEXT NOT NULL,
	cpu TEXT NOT NULL,
	ram_gb INTEGER NOT NULL,
	storage TEXT NOT NULL,
	storage_type TEXT NOT NULL,
	price_monthly REAL NOT NULL,
	order_url TEXT NOT NULL,
	watched INTEGER NOT NULL DEFAULT 1,
	stock INTEGER NOT NULL DEFAULT 0,
	previous_stock INTEGER NOT NULL DEFAULT 0,
	checked INTEGER NOT NULL DEFAULT 0,
	alert_pending INTEGER NOT NULL DEFAULT 0,
	alert_count INTEGER NOT NULL DEFAULT 0,
	last_alert_at TEXT,
	next_alert_at TEXT,
	last_seen_at TEXT,
	last_changed_at TEXT
);

CREATE TABLE IF NOT EXISTS sessions (
	token_hash TEXT PRIMARY KEY,
	expires_at TEXT NOT NULL,
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS checks (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	started_at TEXT NOT NULL,
	finished_at TEXT NOT NULL,
	ok INTEGER NOT NULL,
	error TEXT NOT NULL DEFAULT '',
	available INTEGER NOT NULL,
	total_products INTEGER NOT NULL,
	new_stock INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS stock_events (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	product_id TEXT NOT NULL,
	old_stock INTEGER NOT NULL,
	new_stock INTEGER NOT NULL,
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS notifications (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	status TEXT NOT NULL,
	subject TEXT NOT NULL,
	recipients TEXT NOT NULL,
	product_ids TEXT NOT NULL,
	error TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS logs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	level TEXT NOT NULL,
	message TEXT NOT NULL,
	created_at TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_checks_finished_at ON checks(finished_at DESC);
CREATE INDEX IF NOT EXISTS idx_stock_events_created_at ON stock_events(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_notifications_created_at ON notifications(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_logs_created_at ON logs(created_at DESC);
`
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("initialize schema: %w", err)
	}

	if err := s.migrateProducts(); err != nil {
		return err
	}

	for _, product := range catalog {
		if _, err := s.db.Exec(`
INSERT INTO products (
	id, name, location_code, location_name, cpu, ram_gb, storage, storage_type, price_monthly, order_url
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
	name = excluded.name,
	location_code = excluded.location_code,
	location_name = excluded.location_name,
	cpu = excluded.cpu,
	ram_gb = excluded.ram_gb,
	storage = excluded.storage,
	storage_type = excluded.storage_type,
	price_monthly = excluded.price_monthly,
	order_url = excluded.order_url
`, product.ID, product.Name, product.LocationCode, product.LocationName, product.CPU,
			product.RAMGB, product.Storage, product.StorageType, product.PriceMonthly, product.OrderURL,
		); err != nil {
			return fmt.Errorf("seed product %s: %w", product.ID, err)
		}
	}
	return nil
}

func (s *Store) migrateProducts() error {
	columns := map[string]string{
		"alert_count":   "ALTER TABLE products ADD COLUMN alert_count INTEGER NOT NULL DEFAULT 0",
		"last_alert_at": "ALTER TABLE products ADD COLUMN last_alert_at TEXT",
		"next_alert_at": "ALTER TABLE products ADD COLUMN next_alert_at TEXT",
	}
	for column, migration := range columns {
		var exists int
		if err := s.db.QueryRow(
			"SELECT COUNT(*) FROM pragma_table_info('products') WHERE name = ?",
			column,
		).Scan(&exists); err != nil {
			return fmt.Errorf("check products column %s: %w", column, err)
		}
		if exists == 0 {
			if _, err := s.db.Exec(migration); err != nil {
				return fmt.Errorf("add products column %s: %w", column, err)
			}
		}
	}
	return nil
}

func (s *Store) Bootstrap(adminPassword, apiToken string) (string, error) {
	existingHash, err := s.getSetting("admin_hash")
	if err != nil {
		return "", err
	}

	initialPassword := ""
	if existingHash == "" {
		if adminPassword == "" {
			adminPassword = randomPassword(18)
			initialPassword = adminPassword
		}
		salt := randomHex(16)
		hash := hashPassword(adminPassword, salt)
		if err := s.setSettings(map[string]string{
			"admin_hash": hash,
			"admin_salt": salt,
		}); err != nil {
			return "", err
		}
	}

	existingToken, err := s.getSetting("api_token")
	if err != nil {
		return "", err
	}
	if existingToken == "" {
		if apiToken == "" {
			apiToken = "shm_" + randomHex(24)
		}
		if err := s.setSetting("api_token", apiToken); err != nil {
			return "", err
		}
	}

	defaults := map[string]string{
		"poll_interval_seconds": fmt.Sprintf("%d", defaultPollSeconds),
		"smtp_config":           mustJSON(defaultSMTPConfig()),
	}
	for key, value := range defaults {
		current, err := s.getSetting(key)
		if err != nil {
			return "", err
		}
		if current != "" {
			continue
		}
		if _, err := s.db.Exec(`INSERT OR IGNORE INTO settings(key, value) VALUES (?, ?)`, key, value); err != nil {
			return "", err
		}
	}
	return initialPassword, nil
}

func defaultSMTPConfig() SMTPConfig {
	return SMTPConfig{
		Port:          587,
		Security:      "starttls",
		SubjectPrefix: "[Shock Stock]",
		Recipients:    []string{},
	}
}

func (s *Store) GetSettings() (Settings, error) {
	pollRaw, err := s.getSetting("poll_interval_seconds")
	if err != nil {
		return Settings{}, err
	}
	smtpRaw, err := s.getSetting("smtp_config")
	if err != nil {
		return Settings{}, err
	}
	settings := Settings{PollIntervalSeconds: defaultPollSeconds, SMTP: defaultSMTPConfig()}
	if pollRaw != "" {
		if _, err := fmt.Sscanf(pollRaw, "%d", &settings.PollIntervalSeconds); err != nil {
			settings.PollIntervalSeconds = defaultPollSeconds
		}
	}
	if smtpRaw != "" {
		if err := json.Unmarshal([]byte(smtpRaw), &settings.SMTP); err != nil {
			return Settings{}, fmt.Errorf("decode SMTP settings: %w", err)
		}
	}
	return settings, nil
}

func (s *Store) SaveSettings(settings Settings) error {
	return s.setSettings(map[string]string{
		"poll_interval_seconds": fmt.Sprintf("%d", settings.PollIntervalSeconds),
		"smtp_config":           mustJSON(settings.SMTP),
	})
}

func (s *Store) SetPassword(password string) error {
	salt := randomHex(16)
	if err := s.setSettings(map[string]string{
		"admin_hash": hashPassword(password, salt),
		"admin_salt": salt,
	}); err != nil {
		return err
	}
	_, err := s.db.Exec("DELETE FROM sessions")
	return err
}

func (s *Store) VerifyPassword(password string) (bool, error) {
	hash, err := s.getSetting("admin_hash")
	if err != nil {
		return false, err
	}
	salt, err := s.getSetting("admin_salt")
	if err != nil {
		return false, err
	}
	if hash == "" || salt == "" {
		return false, errors.New("admin password is not initialized")
	}
	return constantTimeStringEqual(hash, hashPassword(password, salt)), nil
}

func (s *Store) APIToken() (string, error) {
	return s.getSetting("api_token")
}

func (s *Store) RotateAPIToken() (string, error) {
	token := "shm_" + randomHex(24)
	return token, s.setSetting("api_token", token)
}

func (s *Store) CreateSession() (string, error) {
	token := randomHex(32)
	now := time.Now().UTC()
	expires := now.Add(24 * time.Hour)
	if _, err := s.db.Exec(
		"INSERT INTO sessions(token_hash, expires_at, created_at) VALUES (?, ?, ?)",
		hashToken(token), expires.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano),
	); err != nil {
		return "", err
	}
	_, _ = s.db.Exec("DELETE FROM sessions WHERE expires_at < ?", now.Format(time.RFC3339Nano))
	return token, nil
}

func (s *Store) ValidSession(token string) bool {
	if token == "" {
		return false
	}
	var expiresRaw string
	err := s.db.QueryRow(
		"SELECT expires_at FROM sessions WHERE token_hash = ?",
		hashToken(token),
	).Scan(&expiresRaw)
	if err != nil {
		return false
	}
	expires, err := time.Parse(time.RFC3339Nano, expiresRaw)
	if err != nil || time.Now().UTC().After(expires) {
		_, _ = s.db.Exec("DELETE FROM sessions WHERE token_hash = ?", hashToken(token))
		return false
	}
	return true
}

func (s *Store) DeleteSession(token string) {
	if token == "" {
		return
	}
	_, _ = s.db.Exec("DELETE FROM sessions WHERE token_hash = ?", hashToken(token))
}

func (s *Store) ListProducts(ctx context.Context) ([]ProductView, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, name, location_code, location_name, cpu, ram_gb, storage, storage_type,
	price_monthly, order_url, watched, stock, previous_stock, checked, alert_pending,
	alert_count, last_alert_at, next_alert_at, last_seen_at, last_changed_at
FROM products
ORDER BY location_name, name
`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	products := make([]ProductView, 0, len(catalog))
	for rows.Next() {
		var view ProductView
		var watched, checked, pending int
		var lastAlert, nextAlert, lastSeen, lastChanged sql.NullString
		if err := rows.Scan(
			&view.ID, &view.Name, &view.LocationCode, &view.LocationName, &view.CPU,
			&view.RAMGB, &view.Storage, &view.StorageType, &view.PriceMonthly, &view.OrderURL,
			&watched, &view.Stock, &view.PreviousStock, &checked, &pending, &view.AlertCount,
			&lastAlert, &nextAlert,
			&lastSeen, &lastChanged,
		); err != nil {
			return nil, err
		}
		view.Watched = watched == 1
		view.Checked = checked == 1
		view.AlertPending = pending == 1
		view.LastAlertAt = parseNullableTime(lastAlert)
		view.NextAlertAt = parseNullableTime(nextAlert)
		view.LastSeenAt = parseNullableTime(lastSeen)
		view.LastChangedAt = parseNullableTime(lastChanged)
		products = append(products, view)
	}
	return products, rows.Err()
}

func (s *Store) UpdateWatch(ctx context.Context, ids []string, watched bool) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	updated := 0
	for _, id := range ids {
		if _, ok := catalogByID[id]; !ok {
			continue
		}
		var result sql.Result
		if watched {
			result, err = tx.ExecContext(ctx, "UPDATE products SET watched = 1 WHERE id = ?", id)
		} else {
			result, err = tx.ExecContext(ctx, `
UPDATE products
SET watched = 0, alert_pending = 0, alert_count = 0,
	last_alert_at = NULL, next_alert_at = NULL
WHERE id = ?
`, id)
		}
		if err != nil {
			return 0, err
		}
		count, _ := result.RowsAffected()
		updated += int(count)
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return updated, nil
}

func (s *Store) ApplyStock(ctx context.Context, stock map[string]int) (CheckOutcome, error) {
	now := time.Now().UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return CheckOutcome{}, err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `
SELECT id, watched, stock, checked, alert_pending, alert_count,
	last_alert_at, next_alert_at
FROM products ORDER BY id
`)
	if err != nil {
		return CheckOutcome{}, err
	}

	type state struct {
		id         string
		watched    bool
		stock      int
		checked    bool
		pending    bool
		alertCount int
		lastAlert  *time.Time
		nextAlert  *time.Time
	}
	states := make(map[string]state, len(catalog))
	for rows.Next() {
		var item state
		var watched, checked, pending int
		var lastAlert, nextAlert sql.NullString
		if err := rows.Scan(
			&item.id, &watched, &item.stock, &checked, &pending, &item.alertCount,
			&lastAlert, &nextAlert,
		); err != nil {
			rows.Close()
			return CheckOutcome{}, err
		}
		item.watched = watched == 1
		item.checked = checked == 1
		item.pending = pending == 1
		item.lastAlert = parseNullableTime(lastAlert)
		item.nextAlert = parseNullableTime(nextAlert)
		states[item.id] = item
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return CheckOutcome{}, err
	}
	rows.Close()

	newStockIDs := make([]string, 0)
	available := 0
	for id, next := range stock {
		item, exists := states[id]
		if !exists || next < 0 {
			continue
		}
		old := item.stock
		if next > 0 {
			available++
		}
		pending := item.pending
		alertCount := item.alertCount
		lastAlert := item.lastAlert
		nextAlert := item.nextAlert
		if next == 0 {
			pending = false
			alertCount = 0
			lastAlert = nil
			nextAlert = nil
		} else if !item.checked || old == 0 {
			if item.watched {
				pending = true
				alertCount = 0
				lastAlert = nil
				nextAlert = nil
				newStockIDs = append(newStockIDs, id)
			} else {
				pending = false
				alertCount = 0
				lastAlert = nil
				nextAlert = nil
			}
		} else if item.watched {
			alertCount = item.alertCount
			lastAlert = item.lastAlert
			nextAlert = item.nextAlert
			due := alertCount == 0 || nextAlert == nil || !now.Before(*nextAlert)
			pending = item.pending && due
			if !item.pending && alertCount > 0 && alertCount < maxAlertsPerEvent &&
				nextAlert != nil && !now.Before(*nextAlert) {
				pending = true
			}
		} else {
			pending = false
			alertCount = 0
			lastAlert = nil
			nextAlert = nil
		}

		changedAt := ""
		if !item.checked || old != next {
			changedAt = now.Format(time.RFC3339Nano)
		}
		if item.checked && old != next {
			if _, err := tx.ExecContext(ctx, `
INSERT INTO stock_events(product_id, old_stock, new_stock, created_at)
VALUES (?, ?, ?, ?)
`, id, old, next, now.Format(time.RFC3339Nano)); err != nil {
				return CheckOutcome{}, err
			}
		}

		if changedAt != "" {
			_, err = tx.ExecContext(ctx, `
UPDATE products
SET previous_stock = stock, stock = ?, checked = 1, alert_pending = ?, alert_count = ?,
	last_alert_at = ?, next_alert_at = ?, last_seen_at = ?, last_changed_at = ?
WHERE id = ?
`, next, boolInt(pending), alertCount, formatNullableTime(lastAlert),
				formatNullableTime(nextAlert), now.Format(time.RFC3339Nano), changedAt, id)
		} else {
			_, err = tx.ExecContext(ctx, `
UPDATE products
SET previous_stock = stock, stock = ?, checked = 1, alert_pending = ?, alert_count = ?,
	last_alert_at = ?, next_alert_at = ?, last_seen_at = ?
WHERE id = ?
`, next, boolInt(pending), alertCount, formatNullableTime(lastAlert),
				formatNullableTime(nextAlert), now.Format(time.RFC3339Nano), id)
		}
		if err != nil {
			return CheckOutcome{}, err
		}
	}

	var total, checkedCount int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*), SUM(checked) FROM products").Scan(&total, &checkedCount); err != nil {
		return CheckOutcome{}, err
	}
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM products WHERE stock > 0").Scan(&available); err != nil {
		return CheckOutcome{}, err
	}

	record := CheckRecord{
		StartedAt:     now,
		FinishedAt:    now,
		OK:            true,
		Available:     available,
		TotalProducts: total,
		NewStock:      len(newStockIDs),
	}
	result, err := tx.ExecContext(ctx, `
INSERT INTO checks(started_at, finished_at, ok, error, available, total_products, new_stock)
VALUES (?, ?, 1, '', ?, ?, ?)
`, now.Format(time.RFC3339Nano), now.Format(time.RFC3339Nano), available, total, len(newStockIDs))
	if err != nil {
		return CheckOutcome{}, err
	}
	record.ID, _ = result.LastInsertId()

	sort.Strings(newStockIDs)
	pendingIDs := make([]string, 0)
	pendingRows, err := tx.QueryContext(ctx, "SELECT id FROM products WHERE alert_pending = 1 ORDER BY id")
	if err != nil {
		return CheckOutcome{}, err
	}
	for pendingRows.Next() {
		var id string
		if err := pendingRows.Scan(&id); err != nil {
			pendingRows.Close()
			return CheckOutcome{}, err
		}
		pendingIDs = append(pendingIDs, id)
	}
	pendingRows.Close()

	if _, err := tx.ExecContext(ctx, `
DELETE FROM stock_events WHERE id NOT IN (SELECT id FROM stock_events ORDER BY id DESC LIMIT 5000)
`); err != nil {
		return CheckOutcome{}, err
	}
	if _, err := tx.ExecContext(ctx, `
DELETE FROM checks WHERE id NOT IN (SELECT id FROM checks ORDER BY id DESC LIMIT 5000)
`); err != nil {
		return CheckOutcome{}, err
	}
	if err := tx.Commit(); err != nil {
		return CheckOutcome{}, err
	}
	return CheckOutcome{Record: record, NewStockIDs: newStockIDs, PendingAlertIDs: pendingIDs}, nil
}

func (s *Store) RecordFailedCheck(ctx context.Context, started time.Time, checkErr error) (CheckRecord, error) {
	finished := time.Now().UTC()
	record := CheckRecord{
		StartedAt:  started,
		FinishedAt: finished,
		OK:         false,
		Error:      checkErr.Error(),
	}
	result, err := s.db.ExecContext(ctx, `
INSERT INTO checks(started_at, finished_at, ok, error, available, total_products, new_stock)
VALUES (?, ?, 0, ?, 0, 0, 0)
`, started.Format(time.RFC3339Nano), finished.Format(time.RFC3339Nano), checkErr.Error())
	if err != nil {
		return record, err
	}
	record.ID, _ = result.LastInsertId()
	return record, nil
}

func (s *Store) LatestCheck(ctx context.Context) (*CheckRecord, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, started_at, finished_at, ok, error, available, total_products, new_stock
FROM checks ORDER BY id DESC LIMIT 1
`)
	var record CheckRecord
	var ok int
	var started, finished string
	if err := row.Scan(
		&record.ID, &started, &finished, &ok, &record.Error,
		&record.Available, &record.TotalProducts, &record.NewStock,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	record.OK = ok == 1
	record.StartedAt, _ = time.Parse(time.RFC3339Nano, started)
	record.FinishedAt, _ = time.Parse(time.RFC3339Nano, finished)
	return &record, nil
}

func (s *Store) ListStockEvents(ctx context.Context, limit int) ([]StockEvent, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT e.id, e.product_id, p.name, e.old_stock, e.new_stock, e.created_at
FROM stock_events e
JOIN products p ON p.id = e.product_id
ORDER BY e.id DESC LIMIT ?
`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]StockEvent, 0, limit)
	for rows.Next() {
		var event StockEvent
		var created string
		if err := rows.Scan(
			&event.ID, &event.ProductID, &event.Product, &event.OldStock,
			&event.NewStock, &created,
		); err != nil {
			return nil, err
		}
		event.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *Store) ListNotifications(ctx context.Context, limit int) ([]NotificationRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, status, subject, recipients, product_ids, error, created_at
FROM notifications ORDER BY id DESC LIMIT ?
`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := make([]NotificationRecord, 0, limit)
	for rows.Next() {
		var record NotificationRecord
		var created string
		if err := rows.Scan(
			&record.ID, &record.Status, &record.Subject, &record.Recipients,
			&record.ProductIDs, &record.Error, &created,
		); err != nil {
			return nil, err
		}
		record.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *Store) RecordNotification(ctx context.Context, status, subject string, recipients, productIDs []string, sendErr error) error {
	errText := ""
	if sendErr != nil {
		errText = sendErr.Error()
	}
	_, err := s.db.ExecContext(ctx, `
INSERT INTO notifications(status, subject, recipients, product_ids, error, created_at)
VALUES (?, ?, ?, ?, ?, ?)
`, status, subject, strings.Join(recipients, ", "), strings.Join(productIDs, ","),
		errText, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
DELETE FROM notifications WHERE id NOT IN (SELECT id FROM notifications ORDER BY id DESC LIMIT 2000)
`)
	return err
}

func (s *Store) MarkAlertsSent(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range ids {
		var oldCount int
		if err := tx.QueryRowContext(ctx,
			"SELECT alert_count FROM products WHERE id = ?", id,
		).Scan(&oldCount); err != nil {
			return err
		}

		newCount := oldCount + 1
		var nextAlert any
		switch newCount {
		case 1:
			nextAlert = time.Now().UTC().Add(10 * time.Minute).Format(time.RFC3339Nano)
		case 2:
			nextAlert = time.Now().UTC().Add(30 * time.Minute).Format(time.RFC3339Nano)
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE products
SET alert_count = ?, alert_pending = 0, last_alert_at = ?, next_alert_at = ?
WHERE id = ?
`, newCount, time.Now().UTC().Format(time.RFC3339Nano), nextAlert, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) RecordLog(ctx context.Context, level, message string) error {
	if _, err := s.db.ExecContext(ctx, `
INSERT INTO logs(level, message, created_at) VALUES (?, ?, ?)
`, level, message, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `
DELETE FROM logs WHERE id NOT IN (SELECT id FROM logs ORDER BY id DESC LIMIT 2000)
`)
	return err
}

func (s *Store) ListLogs(ctx context.Context, limit int) ([]LogRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id, level, message, created_at FROM logs ORDER BY id DESC LIMIT ?
`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := make([]LogRecord, 0, limit)
	for rows.Next() {
		var record LogRecord
		var created string
		if err := rows.Scan(&record.ID, &record.Level, &record.Message, &created); err != nil {
			return nil, err
		}
		record.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		records = append(records, record)
	}
	return records, rows.Err()
}

func (s *Store) getSetting(key string) (string, error) {
	var value string
	err := s.db.QueryRow("SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}

func (s *Store) setSetting(key, value string) error {
	return s.setSettings(map[string]string{key: value})
}

func (s *Store) setSettings(values map[string]string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for key, value := range values {
		if _, err := tx.Exec(`
INSERT INTO settings(key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value
`, key, value); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func parseNullableTime(value sql.NullString) *time.Time {
	if !value.Valid || value.String == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value.String)
	if err != nil {
		return nil
	}
	return &parsed
}

func formatNullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func randomHex(bytes int) string {
	buf := make([]byte, bytes)
	if _, err := rand.Read(buf); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	return hex.EncodeToString(buf)
}

func randomPassword(length int) string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789"
	buf := make([]byte, length)
	random := make([]byte, length)
	if _, err := rand.Read(random); err != nil {
		panic("crypto/rand unavailable: " + err.Error())
	}
	for i := range buf {
		buf[i] = alphabet[int(random[i])%len(alphabet)]
	}
	return string(buf)
}

func hashToken(token string) string {
	sum := hashSHA256([]byte(token))
	return hex.EncodeToString(sum)
}

func mustJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}
