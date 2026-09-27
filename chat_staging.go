package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"fiatjaf.com/nostr"
	"github.com/BitcoinWalk/relay/internal/chatstate"
)

func runChat() error {
	mode := env("RELAY_CHAT_MODE", "")
	if mode != "staging" && mode != "production" {
		return errors.New("RELAY_CHAT_MODE must be staging or production")
	}
	listen := env("RELAY_CHAT_LISTEN", "127.0.0.1:3335")
	host, _, err := net.SplitHostPort(listen)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return errors.New("chat relay must bind a loopback IP")
	}
	public, err := url.Parse(os.Getenv("RELAY_CHAT_URL"))
	if err != nil || public.Scheme != "wss" || public.Hostname() == "" || public.User != nil || public.RawQuery != "" || public.Fragment != "" || public.Path != "" {
		return errors.New("RELAY_CHAT_URL must be an explicit wss origin")
	}
	dbPath, keyPath := os.Getenv("RELAY_CHAT_DB"), os.Getenv("RELAY_CHAT_KEY_FILE")
	if !filepath.IsAbs(dbPath) || !filepath.IsAbs(keyPath) {
		return errors.New("explicit absolute chat database and credential paths required")
	}
	key, err := readRelayCredential(keyPath)
	if err != nil {
		return err
	}
	s, err := chatstate.OpenSigned(dbPath, nostr.MustPubKeyFromHex(adminHex), key)
	if err != nil {
		return err
	}
	defer s.Close()
	if err := s.VerifyRecovery(); err != nil {
		return err
	}
	r := newChatRelay(s)
	r.ServiceURL = public.String()
	if mode == "production" {
		r.Info.Name = "BitcoinWalk Community"
		r.Info.Description = "The members-only global community relay for free-tier BitcoinWalk cities."
		r.Info.Version = "bitcoinwalk-chat-0.2.0"
	} else {
		r.Info.Name = "BitcoinWalk chat staging"
	}
	r.Router().HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		// Key was verified and the database was opened successfully. Actual
		// backup writes/restore tests are separate from this liveness endpoint.
		fmt.Fprintf(w, "{\"status\":\"ok\",\"mode\":\"persistent-chat-%s\"}\n", mode)
	})
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	server := &http.Server{Addr: listen, Handler: r, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
}

// Offline maintenance only: OpenSigned obtains the database lock, so this will
// refuse to run against an active service rather than copying a changing file.
func backupChat() error {
	if !filepath.IsAbs(os.Getenv("RELAY_CHAT_DB")) || !filepath.IsAbs(os.Getenv("RELAY_CHAT_BACKUP")) {
		return errors.New("absolute database and backup paths required")
	}
	if _, err := os.Stat(os.Getenv("RELAY_CHAT_DB")); err != nil {
		return errors.New("existing database required for backup")
	}
	key, err := readRelayCredential(os.Getenv("RELAY_CHAT_KEY_FILE"))
	if err != nil {
		return err
	}
	s, err := chatstate.OpenSigned(os.Getenv("RELAY_CHAT_DB"), nostr.MustPubKeyFromHex(adminHex), key)
	if err != nil {
		return err
	}
	defer s.Close()
	if err := s.VerifyRecovery(); err != nil {
		return err
	}
	return s.Backup(os.Getenv("RELAY_CHAT_BACKUP"))
}
