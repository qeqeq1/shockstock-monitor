package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"testing"
)

func TestListenWithFallbackSelectsNextPort(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()

	port := occupied.Addr().(*net.TCPAddr).Port
	listener, err := listenWithFallback(
		fmt.Sprintf("127.0.0.1:%d", port),
		true,
		10,
		log.New(io.Discard, "", 0),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	selected := listener.Addr().(*net.TCPAddr).Port
	if selected == port {
		t.Fatalf("selected occupied port %d", port)
	}
	if selected <= port || selected > port+10 {
		t.Fatalf("selected port %d outside expected range", selected)
	}
}

func TestListenWithFallbackCanBeDisabled(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()

	port := occupied.Addr().(*net.TCPAddr).Port
	listener, err := listenWithFallback(
		fmt.Sprintf("127.0.0.1:%d", port),
		false,
		10,
		log.New(io.Discard, "", 0),
	)
	if err == nil {
		listener.Close()
		t.Fatal("expected occupied port to fail when fallback is disabled")
	}
}
