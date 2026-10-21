package main

import (
	"context"
	"errors"
	"iter"
	"net/url"
	"os"
	"path/filepath"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/khatru"
	"github.com/BitcoinWalk/relay/internal/chatstate"
)

func chatCommandKind(kind nostr.Kind) bool {
	switch kind {
	case 7, 9, 9000, 9001, 9002, 9007, 9021, 9022:
		return true
	default:
		return false
	}
}

func chatScopedFilter(filter nostr.Filter) bool {
	if metadataFilter(filter) {
		return true
	}
	if len(filter.Kinds) == 0 {
		return false
	}
	hasGroup := len(filter.Tags["h"]) > 0
	chatKind := false
	for _, kind := range filter.Kinds {
		if kind >= 39000 && kind <= 39003 || kind == 9000 || kind == 9001 || kind == 9 || kind == 1068 || kind == 7 || kind == 11 {
			chatKind = true
			continue
		}
		if hasGroup && kind == 5 {
			chatKind = true
			continue
		}
	}
	return chatKind
}

func combinedSupportedNIPs(existing []any) []any {
	result := append([]any(nil), existing...)
	for _, required := range []int{1, 11, 29, 42} {
		found := false
		for _, value := range result {
			if value == required {
				found = true
				break
			}
		}
		if !found {
			result = append(result, required)
		}
	}
	return result
}

// attachChatRelay composes the bounded NIP-29 profile with an existing city
// event relay. Event and chat storage remain separate, and malformed chat
// requests cannot fall through to the public event store.
func attachChatRelay(target *khatru.Relay, store *chatstate.Store) error {
	if target == nil || store == nil {
		return errors.New("combined chat requires relay and storage")
	}
	chat := newChatRelay(store)

	previousOnEvent := target.OnEvent
	target.OnEvent = func(ctx context.Context, event nostr.Event) (bool, string) {
		if chatCommandKind(event.Kind) {
			return chat.OnEvent(ctx, event)
		}
		if previousOnEvent == nil {
			return false, ""
		}
		return previousOnEvent(ctx, event)
	}
	previousStoreEvent := target.StoreEvent
	target.StoreEvent = func(ctx context.Context, event nostr.Event) error {
		if chatCommandKind(event.Kind) {
			return chat.StoreEvent(ctx, event)
		}
		if previousStoreEvent == nil {
			return errors.New("city event storage unavailable")
		}
		return previousStoreEvent(ctx, event)
	}
	previousOnRequest := target.OnRequest
	target.OnRequest = func(ctx context.Context, filter nostr.Filter) (bool, string) {
		if chatScopedFilter(filter) {
			return chat.OnRequest(ctx, filter)
		}
		if previousOnRequest == nil {
			return false, ""
		}
		return previousOnRequest(ctx, filter)
	}
	previousOnCount := target.OnCount
	target.OnCount = func(ctx context.Context, filter nostr.Filter) (bool, string) {
		if chatScopedFilter(filter) {
			return true, "restricted: chat counts are unavailable"
		}
		if previousOnCount == nil {
			return false, ""
		}
		return previousOnCount(ctx, filter)
	}
	previousQueryStored := target.QueryStored
	target.QueryStored = func(ctx context.Context, filter nostr.Filter) iter.Seq[nostr.Event] {
		if chatScopedFilter(filter) {
			return chat.QueryStored(ctx, filter)
		}
		if previousQueryStored == nil {
			return func(func(nostr.Event) bool) {}
		}
		return previousQueryStored(ctx, filter)
	}
	previousOnAuth := target.OnAuth
	target.OnAuth = func(ctx context.Context, key nostr.PubKey) {
		if previousOnAuth != nil {
			previousOnAuth(ctx, key)
		}
		chat.OnAuth(ctx, key)
	}
	previousOnDisconnect := target.OnDisconnect
	target.OnDisconnect = func(ctx context.Context) {
		chat.OnDisconnect(ctx)
		if previousOnDisconnect != nil {
			previousOnDisconnect(ctx)
		}
	}
	previousOnListenerAdded := target.OnListenerAdded
	target.OnListenerAdded = func(ws *khatru.WebSocket, serial int, id string, filter nostr.Filter) {
		if chatScopedFilter(filter) {
			chat.OnListenerAdded(ws, serial, id, filter)
			return
		}
		if previousOnListenerAdded != nil {
			previousOnListenerAdded(ws, serial, id, filter)
		}
	}
	previousOnListenerRemoved := target.OnListenerRemoved
	target.OnListenerRemoved = func(ws *khatru.WebSocket, serial int, id string, filter nostr.Filter) {
		if chatScopedFilter(filter) {
			chat.OnListenerRemoved(ws, serial, id, filter)
			return
		}
		if previousOnListenerRemoved != nil {
			previousOnListenerRemoved(ws, serial, id, filter)
		}
	}
	previousPreventBroadcast := target.PreventBroadcast
	target.PreventBroadcast = func(ws *khatru.WebSocket, filter nostr.Filter, event nostr.Event) bool {
		if chatCommandKind(event.Kind) || chatScopedFilter(filter) {
			return true
		}
		return previousPreventBroadcast != nil && previousPreventBroadcast(ws, filter, event)
	}
	previousOnEventSaved := target.OnEventSaved
	target.OnEventSaved = func(ctx context.Context, event nostr.Event) {
		if chatCommandKind(event.Kind) {
			chat.OnEventSaved(ctx, event)
			return
		}
		if previousOnEventSaved != nil {
			previousOnEventSaved(ctx, event)
		}
	}
	if target.MaxMessageSize < chat.MaxMessageSize {
		target.MaxMessageSize = chat.MaxMessageSize
	}
	target.Info.SupportedNIPs = combinedSupportedNIPs(target.Info.SupportedNIPs)
	target.Info.Self = chat.Info.Self
	return nil
}

func enableCombinedCityChat(relay *khatru.Relay) (*chatstate.Store, error) {
	if os.Getenv("RELAY_CHAT_COMBINED") != "true" {
		return nil, nil
	}
	public, err := url.Parse(os.Getenv("RELAY_CHAT_URL"))
	if err != nil || public.Scheme != "wss" || public.Hostname() == "" || public.User != nil || public.RawQuery != "" || public.Fragment != "" || public.Path != "" {
		return nil, errors.New("RELAY_CHAT_URL must be an explicit wss origin")
	}
	dbPath, keyPath := os.Getenv("RELAY_CHAT_DB"), os.Getenv("RELAY_CHAT_KEY_FILE")
	if !filepath.IsAbs(dbPath) || !filepath.IsAbs(keyPath) {
		return nil, errors.New("combined chat requires absolute database and credential paths")
	}
	key, err := readRelayCredential(keyPath)
	if err != nil {
		return nil, err
	}
	store, err := chatstate.OpenSigned(dbPath, nostr.MustPubKeyFromHex(adminHex), key)
	if err != nil {
		return nil, err
	}
	if err := store.VerifyRecovery(); err != nil {
		store.Close()
		return nil, err
	}
	if err := attachChatRelay(relay, store); err != nil {
		store.Close()
		return nil, err
	}
	relay.ServiceURL = public.String()
	return store, nil
}
