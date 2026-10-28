package main

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCatalogMapping(t *testing.T) {
	if len(catalog) != 68 {
		t.Fatalf("catalog length = %d, want 68", len(catalog))
	}
	seen := make(map[string]bool, len(catalog))
	for _, product := range catalog {
		if seen[product.ID] {
			t.Fatalf("duplicate product ID %s", product.ID)
		}
		seen[product.ID] = true
		if product.Name == "" || product.LocationCode == "" || product.OrderURL == "" {
			t.Fatalf("incomplete product: %+v", product)
		}
	}
	if catalogByID["117"].Name != "JP-DH1" {
		t.Fatalf("unexpected JP-DH1 mapping: %+v", catalogByID["117"])
	}
	if catalogByID["145"].Name != "ZA-DS2" {
		t.Fatalf("unexpected ZA-DS2 mapping: %+v", catalogByID["145"])
	}
}

func TestBootstrapAndSessions(t *testing.T) {
	store := newTestStore(t, "correct-horse-battery", "shm_test_token")
	defer store.Close()

	ok, err := store.VerifyPassword("correct-horse-battery")
	if err != nil || !ok {
		t.Fatalf("verify password = %v, %v", ok, err)
	}
	ok, err = store.VerifyPassword("wrong")
	if err != nil || ok {
		t.Fatalf("wrong password accepted: %v, %v", ok, err)
	}
	token, err := store.APIToken()
	if err != nil || token != "shm_test_token" {
		t.Fatalf("API token = %q, %v", token, err)
	}
	session, err := store.CreateSession()
	if err != nil || !store.ValidSession(session) {
		t.Fatalf("session not valid: %v", err)
	}
	store.DeleteSession(session)
	if store.ValidSession(session) {
		t.Fatal("deleted session remains valid")
	}
}

func TestStockLifecycleAndAlertPending(t *testing.T) {
	store := newTestStore(t, "correct-horse-battery", "shm_test_token")
	defer store.Close()
	ctx := context.Background()

	first, err := store.ApplyStock(ctx, map[string]int{"117": 0})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.NewStockIDs) != 0 || len(first.PendingAlertIDs) != 0 {
		t.Fatalf("unexpected first check alerts: %+v", first)
	}

	second, err := store.ApplyStock(ctx, map[string]int{"117": 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.NewStockIDs) != 1 || second.NewStockIDs[0] != "117" {
		t.Fatalf("new stock IDs = %v", second.NewStockIDs)
	}
	if len(second.PendingAlertIDs) != 1 || second.PendingAlertIDs[0] != "117" {
		t.Fatalf("pending alert IDs = %v", second.PendingAlertIDs)
	}
	if second.Record.Available != 1 || second.Record.NewStock != 1 {
		t.Fatalf("unexpected check record: %+v", second.Record)
	}

	third, err := store.ApplyStock(ctx, map[string]int{"117": 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(third.NewStockIDs) != 0 {
		t.Fatalf("unchanged stock reported as new: %v", third.NewStockIDs)
	}
	if len(third.PendingAlertIDs) != 1 {
		t.Fatalf("initial alert should remain queued until delivery succeeds: %v", third.PendingAlertIDs)
	}
	if err := store.MarkAlertsSent(ctx, []string{"117"}); err != nil {
		t.Fatal(err)
	}

	fourth, err := store.ApplyStock(ctx, map[string]int{"117": 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(fourth.PendingAlertIDs) != 0 {
		t.Fatalf("sent alert was not cleared: %v", fourth.PendingAlertIDs)
	}

	_, err = store.db.Exec(
		"UPDATE products SET next_alert_at = ? WHERE id = ?",
		time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano),
		"117",
	)
	if err != nil {
		t.Fatal(err)
	}
	fifth, err := store.ApplyStock(ctx, map[string]int{"117": 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(fifth.PendingAlertIDs) != 1 {
		t.Fatalf("due reminder was not queued: %v", fifth.PendingAlertIDs)
	}
	if err := store.MarkAlertsSent(ctx, []string{"117"}); err != nil {
		t.Fatal(err)
	}

	sixth, err := store.ApplyStock(ctx, map[string]int{"117": 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(sixth.PendingAlertIDs) != 0 {
		t.Fatalf("second reminder is not due yet: %v", sixth.PendingAlertIDs)
	}
	_, err = store.db.Exec(
		"UPDATE products SET next_alert_at = ? WHERE id = ?",
		time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano),
		"117",
	)
	if err != nil {
		t.Fatal(err)
	}
	seventh, err := store.ApplyStock(ctx, map[string]int{"117": 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(seventh.PendingAlertIDs) != 1 {
		t.Fatalf("final reminder was not queued: %v", seventh.PendingAlertIDs)
	}
	if err := store.MarkAlertsSent(ctx, []string{"117"}); err != nil {
		t.Fatal(err)
	}
	eighth, err := store.ApplyStock(ctx, map[string]int{"117": 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(eighth.PendingAlertIDs) != 0 {
		t.Fatalf("maximum reminder count was not enforced: %v", eighth.PendingAlertIDs)
	}

	ninth, err := store.ApplyStock(ctx, map[string]int{"117": 0})
	if err != nil {
		t.Fatal(err)
	}
	if ninth.Record.Available != 0 {
		t.Fatalf("available = %d, want 0", ninth.Record.Available)
	}

	// A quick return to stock starts a new event immediately.
	tenth, err := store.ApplyStock(ctx, map[string]int{"117": 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(tenth.NewStockIDs) != 1 || len(tenth.PendingAlertIDs) != 1 {
		t.Fatalf("quick restock was not immediate: %+v", tenth)
	}
	events, err := store.ListStockEvents(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) < 3 {
		t.Fatalf("stock events = %d, want at least 3", len(events))
	}
}

func TestHandlerAuthentication(t *testing.T) {
	store := newTestStore(t, "correct-horse-battery", "shm_test_token")
	defer store.Close()
	app, _, err := NewApp(
		store,
		"https://example.invalid/stock",
		log.New(io.Discard, "", 0),
		"",
		"",
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	webFiles, err := fs.Sub(embeddedWeb, "web")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(app, webFiles)

	request := httptest.NewRequest(http.MethodGet, "/api/v1/stock", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated API status = %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodGet, "/api/v1/status", nil)
	request.Header.Set("Authorization", "Bearer shm_test_token")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("authenticated API status = %d, body=%s", response.Code, response.Body.String())
	}

	body := `{"password":"correct-horse-battery"}`
	request = httptest.NewRequest(http.MethodPost, "/admin/api/login", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("login status = %d, body=%s", response.Code, response.Body.String())
	}
	cookies := response.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("login did not set a session cookie")
	}

	request = httptest.NewRequest(http.MethodGet, "/admin/api/state", nil)
	request.AddCookie(cookies[0])
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("admin state status = %d, body=%s", response.Code, response.Body.String())
	}
	var state AdminState
	if err := json.Unmarshal(response.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Products) != 68 {
		t.Fatalf("admin products = %d, want 68", len(state.Products))
	}
}

func TestFetchStock(t *testing.T) {
	store := newTestStore(t, "correct-horse-battery", "shm_test_token")
	defer store.Close()
	app, _, err := NewApp(store, "https://example.test/stock", log.New(io.Discard, "", 0), "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	app.httpClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Query().Get("_") == "" {
			t.Error("cache-busting query parameter is missing")
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"117":3,"119":0}`)),
			Request:    request,
		}, nil
	})}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stock, err := app.fetchStock(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stock["117"] != 3 || stock["119"] != 0 {
		t.Fatalf("unexpected stock response: %+v", stock)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestSMTPRecipients(t *testing.T) {
	values := normalizeRecipients([]string{
		"one@example.com, two@example.com",
		"TWO@example.com;three@example.com",
	})
	if len(values) != 3 {
		t.Fatalf("recipient count = %d, values=%v", len(values), values)
	}
	config := SMTPConfig{
		Enabled:    true,
		Host:       "smtp.example.com",
		Port:       587,
		Security:   "starttls",
		Username:   "alerts@example.com",
		From:       "Shock Stock <alerts@example.com>",
		Recipients: values,
	}
	if err := validateSMTP(config); err != nil {
		t.Fatal(err)
	}
}

func newTestStore(t *testing.T, password, token string) *Store {
	t.Helper()
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Bootstrap(password, token); err != nil {
		store.Close()
		t.Fatal(err)
	}
	return store
}
