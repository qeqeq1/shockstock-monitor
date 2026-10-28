package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

//go:embed web/*
var embeddedWeb embed.FS

func main() {
	logger := log.New(os.Stdout, "", log.Ldate|log.Ltime|log.LUTC)

	dataDir := envOrDefault("DATA_DIR", "./data")
	listenAddr := envOrDefault("LISTEN_ADDR", envOrDefault("PORT", "8080"))
	if !strings.Contains(listenAddr, ":") {
		listenAddr = ":" + listenAddr
	}
	autoPort := envBool("AUTO_PORT", true)
	autoPortAttempts := envInt("AUTO_PORT_ATTEMPTS", 20)
	stockURL := envOrDefault(
		"STOCK_SOURCE_URL",
		"https://shockhosting.com/api/server-stock.php",
	)

	store, err := OpenStore(dataDir)
	if err != nil {
		logger.Fatalf("initialize database: %v", err)
	}
	defer store.Close()

	app, initialPassword, err := NewApp(
		store,
		stockURL,
		logger,
		os.Getenv("ADMIN_PASSWORD"),
		os.Getenv("RESET_ADMIN_PASSWORD"),
		os.Getenv("API_TOKEN"),
	)
	if err != nil {
		logger.Fatalf("initialize app: %v", err)
	}
	if initialPassword != "" {
		logger.Printf("INITIAL ADMIN PASSWORD: %s", initialPassword)
		logger.Printf("请登录后立即在“系统”页面修改管理密码")
	}

	webFiles, err := fs.Sub(embeddedWeb, "web")
	if err != nil {
		logger.Fatalf("load embedded web files: %v", err)
	}

	rootContext, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()
	go app.RunScheduler(rootContext)

	server := &http.Server{
		Handler:           NewHandler(app, webFiles),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	listener, err := listenWithFallback(listenAddr, autoPort, autoPortAttempts, logger)
	if err != nil {
		logger.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	serverErrors := make(chan error, 1)
	go func() {
		logger.Printf("Shock Stock listening on %s", listener.Addr().String())
		serverErrors <- server.Serve(listener)
	}()

	select {
	case <-rootContext.Done():
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Fatalf("http server: %v", err)
		}
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownContext); err != nil {
		logger.Printf("shutdown: %v", err)
	}
}

func envOrDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func envBool(key string, fallback bool) bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envInt(key string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(key)))
	if err != nil || value < 1 {
		return fallback
	}
	return value
}

func listenWithFallback(listenAddr string, autoPort bool, attempts int, logger *log.Logger) (net.Listener, error) {
	host, portText, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return nil, fmt.Errorf("invalid LISTEN_ADDR %q: %w", listenAddr, err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 0 || port > 65535 {
		return nil, fmt.Errorf("invalid port in LISTEN_ADDR %q", listenAddr)
	}
	if port == 0 {
		return net.Listen("tcp", listenAddr)
	}
	if !autoPort {
		attempts = 1
	}
	if attempts < 1 {
		attempts = 1
	}

	var lastErr error
	for offset := 0; offset < attempts && port+offset <= 65535; offset++ {
		candidate := net.JoinHostPort(host, strconv.Itoa(port+offset))
		listener, err := net.Listen("tcp", candidate)
		if err == nil {
			if offset > 0 {
				logger.Printf("port %d is unavailable; using %d", port, port+offset)
			}
			return listener, nil
		}
		lastErr = err
		if !isAddressInUse(err) {
			return nil, fmt.Errorf("listen %s: %w", candidate, err)
		}
	}
	return nil, fmt.Errorf(
		"no available port in range %d-%d: %w",
		port,
		port+attempts-1,
		lastErr,
	)
}

func isAddressInUse(err error) bool {
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "address already in use") ||
		strings.Contains(message, "only one usage of each socket address")
}
