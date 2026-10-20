package main

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore/boltdb"
	"fiatjaf.com/nostr/khatru"
	"fiatjaf.com/nostr/khatru/policies"
	"go.etcd.io/bbolt"
)

const (
	inboxGiftWrapKind    nostr.Kind = 1059
	inboxMaxContentBytes            = 48 * 1024
	inboxRetention                  = 30 * 24 * time.Hour
	inboxFutureSkew                 = 10 * time.Minute
)

type inboxPolicy struct {
	limits *chatLimits
	now    func() time.Time
}

func giftWrapRecipient(tags nostr.Tags) (nostr.PubKey, error) {
	var recipient nostr.PubKey
	found := false
	for _, tag := range tags {
		if len(tag) != 2 {
			return nostr.ZeroPK, errors.New("invalid: malformed gift-wrap tag")
		}
		switch tag[0] {
		case "p":
			if found {
				return nostr.ZeroPK, errors.New("invalid: gift wrap must have one recipient")
			}
			parsed, err := nostr.PubKeyFromHex(tag[1])
			if err != nil {
				return nostr.ZeroPK, errors.New("invalid: malformed recipient")
			}
			recipient = parsed
			found = true
		case "expiration":
			if _, err := strconv.ParseInt(tag[1], 10, 64); err != nil {
				return nostr.ZeroPK, errors.New("invalid: malformed expiration")
			}
		default:
			return nostr.ZeroPK, errors.New("invalid: unsupported gift-wrap tag")
		}
	}
	if !found {
		return nostr.ZeroPK, errors.New("invalid: gift wrap requires one recipient")
	}
	return recipient, nil
}

func (p inboxPolicy) event(ctx context.Context, event nostr.Event) (bool, string) {
	if len(khatru.GetAllAuthed(ctx)) == 0 {
		return true, "auth-required: authenticate before publishing"
	}
	if event.Kind != inboxGiftWrapKind {
		return true, "restricted: only NIP-59 gift wraps are accepted"
	}
	if event.Content == "" || len(event.Content) > inboxMaxContentBytes {
		return true, "invalid: gift-wrap content size"
	}
	if _, err := giftWrapRecipient(event.Tags); err != nil {
		return true, err.Error()
	}
	now := p.now()
	created := event.CreatedAt.Time()
	if created.After(now.Add(inboxFutureSkew)) || created.Before(now.Add(-inboxRetention)) {
		return true, "invalid: gift-wrap timestamp outside retention window"
	}
	key := khatru.GetAllAuthed(ctx)[0]
	if !p.limits.allow("inbox-write-ip:"+khatru.GetIP(ctx), 240, time.Minute, now) {
		return true, "rate-limited: inbox connection write limit"
	}
	if !p.limits.allow("inbox-write:"+key.Hex(), 120, time.Minute, now) {
		return true, "rate-limited: inbox write limit"
	}
	return false, ""
}

func inboxFilterRecipient(ctx context.Context, filter nostr.Filter) (nostr.PubKey, bool, string) {
	if len(filter.Kinds) != 1 || filter.Kinds[0] != inboxGiftWrapKind ||
		filter.IDs != nil || filter.Authors != nil || filter.Search != "" ||
		len(filter.Tags) != 1 || len(filter.Tags["p"]) != 1 {
		return nostr.ZeroPK, false, "restricted: request one authenticated recipient inbox"
	}
	recipient, err := nostr.PubKeyFromHex(filter.Tags["p"][0])
	if err != nil {
		return nostr.ZeroPK, false, "invalid: malformed recipient filter"
	}
	if len(khatru.GetAllAuthed(ctx)) == 0 {
		return nostr.ZeroPK, false, "auth-required: authenticate before reading an inbox"
	}
	if !khatru.IsAuthed(ctx, recipient) {
		return nostr.ZeroPK, false, "restricted: read only your authenticated inbox"
	}
	return recipient, true, ""
}

func (p inboxPolicy) request(ctx context.Context, filter nostr.Filter) (bool, string) {
	if _, ok, reason := inboxFilterRecipient(ctx, filter); !ok {
		return true, reason
	}
	if !p.limits.allow("inbox-read:"+khatru.GetIP(ctx), 240, time.Minute, p.now()) {
		return true, "rate-limited: inbox read limit"
	}
	return false, ""
}

func newInboxRelay(dbPath string) (*khatru.Relay, *boltdb.BoltBackend, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0700); err != nil {
		return nil, nil, err
	}
	db := &boltdb.BoltBackend{Path: dbPath}
	if err := db.Init(); err != nil {
		return nil, nil, err
	}
	r := khatru.NewRelay()
	r.Info.Name = env("RELAY_INBOX_NAME", "BitcoinWalk private message inbox")
	r.Info.Description = "Authenticated NIP-17/NIP-59 inbox relay. Gift wraps are readable only by their tagged recipient."
	r.Info.PubKey = func() *nostr.PubKey { key := nostr.MustPubKeyFromHex(adminHex); return &key }()
	r.Info.Version = "bitcoinwalk-inbox-0.9.7"
	r.Info.SupportedNIPs = []any{1, 11, 17, 40, 42, 59}
	r.MaxMessageSize = 64 * 1024
	r.MaxAuthenticatedClients = 1
	r.Negentropy = false
	r.UseEventstore(db, 200)
	policy := inboxPolicy{limits: &chatLimits{}, now: time.Now}
	r.OnEvent = policy.event
	r.OnRequest = policy.request
	r.OnCount = policy.request
	r.RejectConnection = policies.ConnectionRejectionStrictDefaults

	var authMu sync.RWMutex
	authed := map[*khatru.WebSocket]nostr.PubKey{}
	r.OnAuth = func(ctx context.Context, key nostr.PubKey) {
		authMu.Lock()
		authed[khatru.GetConnection(ctx)] = key
		authMu.Unlock()
	}
	r.OnDisconnect = func(ctx context.Context) {
		authMu.Lock()
		delete(authed, khatru.GetConnection(ctx))
		authMu.Unlock()
	}
	r.PreventBroadcast = func(ws *khatru.WebSocket, filter nostr.Filter, event nostr.Event) bool {
		recipient, err := giftWrapRecipient(event.Tags)
		if err != nil || len(filter.Tags["p"]) != 1 || filter.Tags["p"][0] != recipient.Hex() {
			return true
		}
		authMu.RLock()
		key, ok := authed[ws]
		authMu.RUnlock()
		return !ok || key != recipient
	}

	storedQuery := r.QueryStored
	r.QueryStored = func(ctx context.Context, filter nostr.Filter) iter.Seq[nostr.Event] {
		if _, ok, _ := inboxFilterRecipient(ctx, filter); !ok {
			return func(func(nostr.Event) bool) {}
		}
		return storedQuery(ctx, filter)
	}
	r.Router().HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		if err := db.DB.View(func(*bbolt.Tx) error { return nil }); err != nil {
			http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"status":"ok"}`)
	})
	r.Router().HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintln(w, "BitcoinWalk private message inbox relay")
	})
	return r, db, nil
}

func pruneInbox(db *boltdb.BoltBackend, now time.Time) (int, error) {
	removed := 0
	cutoff := nostr.Timestamp(now.Add(-inboxRetention).Unix())
	for {
		ids := make([]nostr.ID, 0, 500)
		for event := range db.QueryEvents(nostr.Filter{
			Kinds: []nostr.Kind{inboxGiftWrapKind}, Until: cutoff,
		}, 500) {
			ids = append(ids, event.ID)
		}
		for _, id := range ids {
			if err := db.DeleteEvent(id); err != nil {
				return removed, err
			}
			removed++
		}
		if len(ids) < 500 {
			return removed, nil
		}
	}
}

func runInbox() error {
	listen := env("RELAY_INBOX_LISTEN", "127.0.0.1:3348")
	if err := validateListenAddress(listen, false); err != nil {
		return err
	}
	relay, db, err := newInboxRelay(env("RELAY_INBOX_DB", "./data/inbox.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	defer relay.DisableExpirationManager()
	if removed, err := pruneInbox(db, time.Now()); err != nil {
		return fmt.Errorf("prune expired inbox events: %w", err)
	} else if removed > 0 {
		log.Printf("Pruned %d expired inbox gift wrap(s)", removed)
	}

	server := &http.Server{
		Addr: listen, Handler: relay, ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if removed, err := pruneInbox(db, time.Now()); err != nil {
					log.Printf("Inbox retention failed")
				} else if removed > 0 {
					log.Printf("Pruned %d expired inbox gift wrap(s)", removed)
				}
			case <-ctx.Done():
				return
			}
		}
	}()
	log.Printf("BitcoinWalk private inbox listening on %s", listen)
	select {
	case err := <-done:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return err
		}
	}
	return nil
}
