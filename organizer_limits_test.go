package main

import (
	"context"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"fiatjaf.com/nostr"
)

func initialWalkForLimitTest(t *testing.T, signer nostr.SecretKey, offset int) nostr.Event {
	t.Helper()
	start := time.Now().Add(48*time.Hour + time.Duration(offset)*time.Minute).Truncate(time.Second)
	event := nostr.Event{Kind: 31923, CreatedAt: nostr.Now() + nostr.Timestamp(offset), Content: "Test walk", Tags: nostr.Tags{
		{"d", cityA + ":" + start.Format("2006-01-02")}, {"title", "BitcoinWalk Test City"}, {"summary", "BitcoinWalk in Test City"}, {"image", "https://example.com/image.jpg"},
		{"start", strconv.FormatInt(start.Unix(), 10)}, {"D", strconv.FormatInt(start.Unix()/86400, 10)}, {"location", "Square"}, {"location", "1,2"}, {"t", "bitcoinwalk"}, {"r", "https://example.com/chat"},
		{"end", strconv.FormatInt(start.Add(time.Hour).Unix(), 10)}, {"start_tzid", "UTC"}, {"end_tzid", "UTC"}, {"i", cityA}, {"bitcoinwalk", "initial-proposal-v1"},
	}}
	if err := event.Sign(signer); err != nil {
		t.Fatal(err)
	}
	return event
}

func TestInitialWalkLimitIgnoresAuthenticationAndExactRetransmission(t *testing.T) {
	admin, creator := nostr.Generate(), nostr.Generate()
	relay, db, err := newRelay(filepath.Join(t.TempDir(), "limits.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	defer relay.DisableExpirationManager()
	enableOrganizers(relay, db, nostr.GetPublicKey(admin))

	proposal := initialWalkForLimitTest(t, creator, 0)
	for range 4 {
		reject, reason := relay.OnEvent(context.Background(), proposal)
		if !reject || !strings.HasPrefix(reason, "auth-required:") {
			t.Fatalf("unauthenticated attempt returned %q", reason)
		}
	}
	accept(t, relay, proposal)
	for range 4 {
		accept(t, relay, proposal)
	}

	accept(t, relay, initialWalkForLimitTest(t, creator, 1))
	accept(t, relay, initialWalkForLimitTest(t, creator, 2))
	fourth := initialWalkForLimitTest(t, creator, 3)
	if _, err := relay.AddEvent(authCtx(t, fourth), fourth); err == nil || !strings.Contains(err.Error(), "initial walk submission limit") {
		t.Fatalf("fourth distinct proposal should be rate-limited, got %v", err)
	}
}
