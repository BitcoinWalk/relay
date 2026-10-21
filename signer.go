package main

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/khatru"
)

const (
	signerReplayTTL = 2 * time.Minute
	signerReplayMax = 256
)

type signerReplayEntry struct {
	event nostr.Event
	at    time.Time
}

// signerReplayBuffer keeps a small, process-local recovery window for Android
// app-to-signer handoffs. Kind 24133 remains encrypted end to end and is never
// written to disk. The strict relay request policy still requires one exact
// recipient, so callers cannot enumerate the buffer.
type signerReplayBuffer struct {
	mu      sync.Mutex
	entries []signerReplayEntry
	ttl     time.Duration
	max     int
	now     func() time.Time
}

func newSignerReplayBuffer(ttl time.Duration, max int, now func() time.Time) *signerReplayBuffer {
	return &signerReplayBuffer{ttl: ttl, max: max, now: now}
}

func (b *signerReplayBuffer) add(event nostr.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	b.pruneLocked(now)
	b.entries = append(b.entries, signerReplayEntry{event: event, at: now})
	if overflow := len(b.entries) - b.max; overflow > 0 {
		b.entries = append([]signerReplayEntry(nil), b.entries[overflow:]...)
	}
}

func (b *signerReplayBuffer) query(filter nostr.Filter) iter.Seq[nostr.Event] {
	b.mu.Lock()
	b.pruneLocked(b.now())
	snapshot := append([]signerReplayEntry(nil), b.entries...)
	b.mu.Unlock()

	limit := filter.Limit
	if limit <= 0 || limit > 8 {
		limit = 8
	}
	return func(yield func(nostr.Event) bool) {
		delivered := 0
		for i := len(snapshot) - 1; i >= 0 && delivered < limit; i-- {
			if !filter.Matches(snapshot[i].event) {
				continue
			}
			delivered++
			if !yield(snapshot[i].event) {
				return
			}
		}
	}
}

func (b *signerReplayBuffer) pruneLocked(now time.Time) {
	cutoff := now.Add(-b.ttl)
	first := 0
	for first < len(b.entries) && b.entries[first].at.Before(cutoff) {
		first++
	}
	if first > 0 {
		b.entries = append([]signerReplayEntry(nil), b.entries[first:]...)
	}
}

// A separate ephemeral transport: no identity key, database or chat access.
// Recipient filters are routing, NOT authorization. Anyone knowing a recipient
// pubkey can observe its encrypted envelopes. Only the endpoints decrypt them.
func newSignerRelay() *khatru.Relay {
	r := khatru.NewRelay()
	r.Info.Name = env("RELAY_SIGNER_NAME", "BitcoinWalk remote signing transport")
	r.Info.Description = "NIP-46 transport with a short memory-only recovery window. Encrypted signing messages are never written to disk."
	r.Info.SupportedNIPs = []any{1, 11, 46}
	r.Info.Version = "bitcoinwalk-remote-signer-0.8.63"
	r.MaxMessageSize = 65536
	r.WriteWait = 2 * time.Second
	r.Negentropy = false
	limits := &chatLimits{}
	replay := newSignerReplayBuffer(signerReplayTTL, signerReplayMax, time.Now)
	var mu sync.Mutex
	counts := map[*khatru.WebSocket]int{}
	type lease struct {
		at     time.Time
		active bool
	}
	connections := map[*http.Request]lease{}
	r.OnConnect = func(ctx context.Context) {
		mu.Lock()
		connections[khatru.GetConnection(ctx).Request] = lease{at: time.Now(), active: true}
		mu.Unlock()
		log.Print("NIP-46 connection opened")
	}
	r.OnListenerRemoved = func(ws *khatru.WebSocket, _ int, _ string, _ nostr.Filter) {
		mu.Lock()
		if counts[ws] > 0 {
			counts[ws]--
		}
		mu.Unlock()
	}
	r.OnDisconnect = func(ctx context.Context) {
		mu.Lock()
		delete(counts, khatru.GetConnection(ctx))
		delete(connections, khatru.GetConnection(ctx).Request)
		mu.Unlock()
		log.Print("NIP-46 connection closed")
	}
	r.RejectConnection = func(req *http.Request) bool {
		if !limits.allow("connect:"+khatru.GetIPFromRequest(req), 30, time.Minute, time.Now()) {
			return true
		}
		mu.Lock()
		defer mu.Unlock()
		for request, l := range connections {
			if !l.active && time.Since(l.at) > time.Minute {
				delete(connections, request)
			}
		}
		if len(connections) >= 128 {
			return true
		}
		connections[req] = lease{at: time.Now()}
		return false
	}
	eventPolicy := func(ctx context.Context, e nostr.Event) (bool, string) {
		if e.Kind != 24133 {
			return true, "restricted: signing messages only"
		}
		if len(e.Content) == 0 || len(e.Content) > 32768 || len(e.Tags) != 1 || len(e.Tags[0]) != 2 || e.Tags[0][0] != "p" {
			return true, "invalid: one recipient and bounded content required"
		}
		if _, err := nostr.PubKeyFromHex(e.Tags[0][1]); err != nil {
			return true, "invalid: recipient"
		}
		now := nostr.Now()
		if e.CreatedAt < now-300 || e.CreatedAt > now+60 {
			return true, "invalid: timestamp"
		}
		if !e.CheckID() || !e.VerifySignature() {
			return true, "invalid: signature"
		}
		if !limits.allow("all-write", 1200, time.Minute, time.Now()) || !limits.allow("write:"+khatru.GetIP(ctx), 240, time.Minute, time.Now()) {
			return true, "rate-limited: signer transport"
		}
		return false, ""
	}
	r.OnEvent = func(ctx context.Context, e nostr.Event) (bool, string) {
		reject, reason := eventPolicy(ctx, e)
		if reject {
			log.Printf("NIP-46 event rejected: %s", reason)
		} else {
			log.Print("NIP-46 event accepted")
		}
		return reject, reason
	}
	r.OnEphemeralEvent = func(_ context.Context, e nostr.Event) {
		replay.add(e)
	}
	r.QueryStored = func(_ context.Context, filter nostr.Filter) iter.Seq[nostr.Event] {
		return replay.query(filter)
	}
	requestPolicy := func(ctx context.Context, f nostr.Filter) (bool, string) {
		if len(f.Kinds) != 1 || f.Kinds[0] != 24133 || len(f.Tags) != 1 || len(f.Tags["p"]) != 1 {
			return true, "restricted: one signing recipient required"
		}
		if _, err := nostr.PubKeyFromHex(f.Tags["p"][0]); err != nil {
			return true, "invalid: recipient"
		}
		mu.Lock()
		defer mu.Unlock()
		n := counts[khatru.GetConnection(ctx)]
		if n >= 8 || !limits.allow("query:"+khatru.GetIP(ctx), 240, time.Minute, time.Now()) {
			return true, "rate-limited: subscriptions"
		}
		counts[khatru.GetConnection(ctx)]++
		return false, ""
	}
	r.OnRequest = func(ctx context.Context, f nostr.Filter) (bool, string) {
		reject, reason := requestPolicy(ctx, f)
		if reject {
			log.Printf("NIP-46 subscription rejected: %s", reason)
		} else {
			log.Print("NIP-46 subscription accepted")
		}
		return reject, reason
	}
	r.Router().HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintln(w, `{"status":"ok","mode":"ephemeral-remote-signer"}`)
	})
	return r
}

func runSignerTransport(mode string) error {
	if mode != "staging" && mode != "remote" {
		return errors.New("RELAY_SIGNER_MODE must be staging or remote")
	}
	listen := "127.0.0.1:3336"
	serviceURL := "wss://chat-staging.bitcoinwalk.org/signer"
	if mode == "remote" {
		listen = "127.0.0.1:3344"
		serviceURL = "wss://remote.bitcoinwalk.org/"
	}
	listen = env("RELAY_SIGNER_LISTEN", listen)
	if err := validateListenAddress(listen, false); err != nil {
		return err
	}
	r := newSignerRelay()
	r.ServiceURL = env("RELAY_SIGNER_SERVICE_URL", serviceURL)
	server := &http.Server{Addr: listen, Handler: r, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16384}
	return server.ListenAndServe()
}

func runSignerAcceptance(target string) error {
	if !strings.HasPrefix(target, "wss://") {
		return errors.New("signer acceptance requires a wss URL")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sender, err := nostr.RelayConnect(ctx, target, nostr.RelayOptions{})
	if err != nil {
		return fmt.Errorf("connect sender: %w", err)
	}
	defer sender.Close()
	receiver, err := nostr.RelayConnect(ctx, target, nostr.RelayOptions{})
	if err != nil {
		return fmt.Errorf("connect receiver: %w", err)
	}
	defer receiver.Close()
	senderKey := nostr.Generate()
	recipient := nostr.GetPublicKey(nostr.Generate()).Hex()
	filter := nostr.Filter{Kinds: []nostr.Kind{24133}, Tags: nostr.TagMap{"p": []string{recipient}}}
	sub, err := receiver.Subscribe(ctx, filter, nostr.SubscriptionOptions{})
	if err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}
	defer sub.Unsub()
	select {
	case <-sub.EndOfStoredEvents:
	case <-ctx.Done():
		return errors.New("signer acceptance did not receive EOSE")
	}
	event := nostr.Event{Kind: 24133, CreatedAt: nostr.Now(), Tags: nostr.Tags{{"p", recipient}}, Content: "bitcoinwalk remote signer acceptance"}
	event.Sign(senderKey)
	if err := sender.Publish(ctx, event); err != nil {
		return fmt.Errorf("publish: %w", err)
	}
	select {
	case got := <-sub.Events:
		if got.ID != event.ID {
			return errors.New("signer acceptance received the wrong live event")
		}
	case <-ctx.Done():
		return errors.New("signer acceptance live delivery timed out")
	}
	recoveryFilter := filter
	recoveryFilter.Limit = 1
	late, err := receiver.Subscribe(ctx, recoveryFilter, nostr.SubscriptionOptions{})
	if err != nil {
		return fmt.Errorf("late subscribe: %w", err)
	}
	defer late.Unsub()
	select {
	case got := <-late.Events:
		if got.ID != event.ID {
			return errors.New("signer acceptance recovered the wrong event")
		}
	case <-ctx.Done():
		return errors.New("signer acceptance recovery check timed out")
	}
	fmt.Printf("live and memory-recovered NIP-46 delivery accepted at %s\n", target)
	return nil
}
