package main

import (
	"context"
	"errors"
	"iter"
	"net/http"
	"strings"
	"sync"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/khatru"
	"fiatjaf.com/nostr/khatru/policies"
	"github.com/BitcoinWalk/relay/internal/chatstate"
)

type chatListener struct {
	serial int
	ws     *khatru.WebSocket
	id     string
	key    nostr.PubKey
	filter nostr.Filter
}

// newChatRelay is deliberately separate from the public organizer relay.
// It is constructed by tests only until metadata/protocol compatibility and
// deployment gates are complete. Never attach the public event store here.
func newChatRelay(store *chatstate.Store) *khatru.Relay {
	r := khatru.NewRelay()
	r.Info.Name = "BitcoinWalk private chat pilot"
	r.Info.Version = "bitcoinwalk-chat-local-0.1.0"
	r.Info.SupportedNIPs = []any{1, 11, 42} // Do not claim complete NIP-29 yet.
	if self, ok := store.RelayIdentity(); ok {
		r.Info.Self = &self
	}
	r.MaxMessageSize = 65536
	r.MaxAuthenticatedClients = 1
	r.WriteWait = 2 * time.Second
	r.Negentropy = false
	limits := &chatLimits{}
	r.RejectConnection = func(req *http.Request) bool {
		return policies.ConnectionRejectionStrictDefaults(req) || !limits.allow("connect:"+khatru.GetIPFromRequest(req), 60, time.Minute, time.Now())
	}
	var mu sync.Mutex
	identities := map[*khatru.WebSocket]nostr.PubKey{}
	listeners := map[int]chatListener{}
	identity := func(ctx context.Context) (nostr.PubKey, bool) {
		mu.Lock()
		defer mu.Unlock()
		key, ok := identities[khatru.GetConnection(ctx)]
		return key, ok
	}
	r.OnAuth = func(ctx context.Context, key nostr.PubKey) {
		ws := khatru.GetConnection(ctx)
		mu.Lock()
		defer mu.Unlock()
		if previous, known := identities[ws]; known && previous == key {
			// Same-user AUTH retries must not silently remove an active feed.
			return
		}
		identities[ws] = key
		// Switching identities invalidates old subscriptions. Never silently
		// transfer subscriptions from one authenticated identity to another.
		for id, sub := range listeners {
			if sub.ws == ws {
				delete(listeners, id)
			}
		}
	}
	r.OnDisconnect = func(ctx context.Context) {
		ws := khatru.GetConnection(ctx)
		mu.Lock()
		defer mu.Unlock()
		delete(identities, ws)
		for id, sub := range listeners {
			if sub.ws == ws {
				delete(listeners, id)
			}
		}
	}
	r.OnEvent = func(ctx context.Context, e nostr.Event) (reject bool, reason string) {
		defer func() {
			if reject && limits.allow("rejection-log", 30, time.Minute, time.Now()) {
				r.Log.Printf("chat publish rejected kind=%d reason=%s", e.Kind, reason)
			}
		}()
		key, ok := identity(ctx)
		if !ok || key != e.PubKey {
			return true, "auth-required: authenticate as author"
		}
		if !limits.allow("write:"+key.Hex(), 60, time.Minute, time.Now()) {
			return true, "rate-limited: chat write limit"
		}
		if e.Kind == 9021 && !limits.allow("join:"+key.Hex(), 10, time.Minute, time.Now()) {
			return true, "rate-limited: chat join limit"
		}
		if e.Kind != 9 && e.Kind != 9000 && e.Kind != 9001 && e.Kind != 9002 && e.Kind != 9007 && e.Kind != 9021 && e.Kind != 9022 {
			return true, "restricted: unsupported chat kind"
		}
		return false, ""
	}
	r.StoreEvent = func(ctx context.Context, e nostr.Event) error {
		key, ok := identity(ctx)
		if !ok {
			return errors.New("auth-required: authenticate as author")
		}
		err := retryChatWrite(ctx, func() error { return store.Apply(e, key, time.Now()) })
		if err != nil && limits.allow("rejection-log", 30, time.Minute, time.Now()) {
			// No content, tags, public keys, event IDs or raw storage errors.
			reason := "storage-or-policy-error"
			for _, allowed := range []string{"invalid: unsupported pilot tag", "invalid: pilot tag too large", "invalid: unexpected target", "invalid: event timestamp outside pilot window", "restricted: current chat membership required", "restricted: banned from this chat", "duplicate: event already processed"} {
				if err.Error() == allowed {
					reason = allowed
					break
				}
			}
			r.Log.Printf("chat publish rejected kind=%d reason=%s", e.Kind, reason)
		}
		return err
	}
	// ReplaceEvent, DeleteEvent, Count/CountHLL and management handlers stay nil.
	r.OnRequest = func(ctx context.Context, f nostr.Filter) (reject bool, reason string) {
		defer func() {
			if reject && limits.allow("read-rejection-log", 30, time.Minute, time.Now()) {
				category := "policy"
				if strings.HasPrefix(reason, "auth-required:") {
					category = "authentication"
				}
				if strings.HasPrefix(reason, "rate-limited:") {
					category = "rate-limit"
				}
				r.Log.Printf("chat read rejected category=%s kinds=%v groups=%d limit=%d", category, f.Kinds, len(f.Tags["h"]), f.Limit)
			}
		}()
		if !limits.allow("query:"+khatru.GetIP(ctx), 240, time.Minute, time.Now()) {
			return true, "rate-limited: chat query limit"
		}
		mu.Lock()
		total := 0
		for _, sub := range listeners {
			if sub.ws == khatru.GetConnection(ctx) {
				total++
			}
		}
		mu.Unlock()
		if total >= 16 {
			return true, "rate-limited: too many subscriptions"
		}
		if metadataFilter(f) {
			mu.Lock()
			defer mu.Unlock()
			n := 0
			for _, sub := range listeners {
				if sub.ws == khatru.GetConnection(ctx) {
					n++
				}
			}
			if n >= 16 {
				return true, "rate-limited: too many subscriptions"
			}
			return false, ""
		}
		key, ok := identity(ctx)
		if !ok {
			return true, "auth-required: authenticate before reading chat"
		}
		if membershipFilter(f, key) {
			return false, ""
		}
		if !chatFilter(f) {
			return true, "restricted: request one chat group and kind 9 explicitly"
		}
		mu.Lock()
		n := 0
		for _, sub := range listeners {
			if sub.ws == khatru.GetConnection(ctx) {
				n++
			}
		}
		mu.Unlock()
		if n >= 16 {
			return true, "rate-limited: too many chat subscriptions"
		}
		for _, group := range f.Tags["h"] {
			if store.CheckMember(group, key) == nil {
				return false, ""
			}
		}
		return true, "restricted: current chat membership required"
	}
	r.QueryStored = func(ctx context.Context, f nostr.Filter) iter.Seq[nostr.Event] {
		return func(yield func(nostr.Event) bool) {
			key, ok := identity(ctx)
			if ok && membershipFilter(f, key) {
				_ = store.DeliverMembershipStatus(key, f, func(e nostr.Event) error {
					mu.Lock()
					defer mu.Unlock()
					if identities[khatru.GetConnection(ctx)] != key {
						return errors.New("identity changed")
					}
					if !yield(e) {
						return errors.New("delivery stopped")
					}
					return nil
				})
				return
			}
			if metadataFilter(f) {
				_ = store.DeliverMetadata(key, f, func(e nostr.Event) error {
					if ctx.Err() != nil {
						return ctx.Err()
					}
					mu.Lock()
					defer mu.Unlock()
					if identities[khatru.GetConnection(ctx)] != key {
						return errors.New("identity changed")
					}
					if !yield(e) {
						return errors.New("delivery stopped")
					}
					return nil
				})
				return
			}
			if !ok || !chatFilter(f) {
				return
			}
			deadline := time.Now().Add(5 * time.Second)
			// The synchronous yield includes Khatru's bounded socket write and
			// stays inside the store's revocation gate; never collect a snapshot.
			err := store.DeliverMultiHistory(key, f, func(e nostr.Event) error {
				if time.Now().After(deadline) {
					return errors.New("history delivery deadline")
				}
				if ctx.Err() != nil {
					return ctx.Err()
				}
				mu.Lock()
				defer mu.Unlock()
				if identities[khatru.GetConnection(ctx)] != key {
					return errors.New("identity changed")
				}
				if f.Matches(e) && !yield(e) {
					return errors.New("delivery stopped")
				}
				return nil
			})
			if err != nil {
				// A partial query must not appear to be a complete history result.
				if ws := khatru.GetConnection(ctx); ws != nil {
					_ = ws.WriteJSON(nostr.ClosedEnvelope{SubscriptionID: khatru.GetSubscriptionID(ctx), Reason: "restricted: history delivery interrupted"})
				}
			}
		}
	}
	r.OnListenerAdded = func(ws *khatru.WebSocket, ssid int, id string, f nostr.Filter) {
		mu.Lock()
		defer mu.Unlock()
		key, ok := identities[ws]
		if (ok && (chatFilter(f) || membershipFilter(f, key))) || metadataFilter(f) {
			listeners[ssid] = chatListener{ssid, ws, id, key, f}
		}
	}
	r.OnListenerRemoved = func(_ *khatru.WebSocket, ssid int, _ string, _ nostr.Filter) {
		mu.Lock()
		delete(listeners, ssid)
		mu.Unlock()
	}
	// Khatru's PreventBroadcast checks permissions BEFORE its socket write;
	// that alone leaves a revocation race. Disable that path completely and
	// perform the write ourselves inside the durable store's delivery gate.
	r.PreventBroadcast = func(*khatru.WebSocket, nostr.Filter, nostr.Event) bool { return true }
	r.OnEventSaved = func(_ context.Context, e nostr.Event) {
		mu.Lock()
		subs := make([]chatListener, 0, len(listeners))
		for _, sub := range listeners {
			subs = append(subs, sub)
		}
		mu.Unlock()
		for _, sub := range subs {
			if membershipFilter(sub.filter, sub.key) {
				if e.Kind == 9 {
					continue
				}
				_ = store.DeliverMembershipStatus(sub.key, sub.filter, func(stored nostr.Event) error {
					mu.Lock()
					defer mu.Unlock()
					if identities[sub.ws] != sub.key {
						return errors.New("identity changed")
					}
					if _, ok := listeners[sub.serial]; !ok {
						return errors.New("subscription closed")
					}
					return sub.ws.WriteJSON(nostr.EventEnvelope{SubscriptionID: &sub.id, Event: stored})
				})
				continue
			}
			if metadataFilter(sub.filter) {
				if e.Kind == 9 {
					continue
				}
				_ = store.DeliverMetadata(sub.key, sub.filter, func(stored nostr.Event) error {
					mu.Lock()
					defer mu.Unlock()
					if identities[sub.ws] != sub.key {
						return errors.New("identity changed")
					}
					if _, active := listeners[sub.serial]; !active {
						return errors.New("subscription closed")
					}
					return sub.ws.WriteJSON(nostr.EventEnvelope{SubscriptionID: &sub.id, Event: stored})
				})
				continue
			}
			if e.Kind != 9 {
				continue
			}
			if !sub.filter.Matches(e) {
				continue
			}
			group := ""
			for _, tag := range e.Tags {
				if len(tag) == 2 && tag[0] == "h" {
					group = tag[1]
					break
				}
			}
			_ = store.DeliverLive(group, sub.key, e.ID, func(stored nostr.Event) error {
				// Recheck subscription existence and identity at final delivery.
				mu.Lock()
				defer mu.Unlock()
				if identities[sub.ws] != sub.key {
					return errors.New("identity changed")
				}
				_, active := listeners[sub.serial]
				if !active {
					return errors.New("subscription closed")
				}
				return sub.ws.WriteJSON(nostr.EventEnvelope{SubscriptionID: &sub.id, Event: stored})
			})
		}
	}
	return r
}

func chatFilter(f nostr.Filter) bool {
	if len(f.Kinds) == 0 || len(f.Kinds) > 5 {
		return false
	}
	for _, kind := range f.Kinds {
		if kind != 9 && kind != 1068 && kind != 5 && kind != 7 && kind != 11 {
			return false
		}
	}
	if len(f.Tags) != 1 || len(f.Tags["h"]) < 1 || len(f.Tags["h"]) > 32 || f.Search != "" || f.Limit < 0 || f.Limit > 200 {
		return false
	}
	seen := make(map[string]bool)
	for _, group := range f.Tags["h"] {
		if group == "" || len(group) > 128 || seen[group] {
			return false
		}
		seen[group] = true
	}
	return true
}

func metadataFilter(f nostr.Filter) bool {
	// DeliverMetadata independently caps results at 200, even when clients ask for 500.
	if len(f.Kinds) == 0 || len(f.Kinds) > 4 || f.Search != "" || f.Limit < 0 {
		return false
	}
	for _, kind := range f.Kinds {
		if kind < 39000 || kind > 39003 {
			return false
		}
	}
	for name := range f.Tags {
		if name != "d" {
			return false
		}
	}
	return true
}

func membershipFilter(f nostr.Filter, key nostr.PubKey) bool {
	if len(f.Kinds) == 0 || len(f.Kinds) > 2 || len(f.Tags) != 2 || len(f.Tags["h"]) != 1 || len(f.Tags["p"]) != 1 || f.Tags["p"][0] != key.Hex() || f.Search != "" || f.Limit < 0 || f.Limit > 200 {
		return false
	}
	for _, kind := range f.Kinds {
		if kind != 9000 && kind != 9001 {
			return false
		}
	}
	return true
}
