package main

import (
	"testing"

	"fiatjaf.com/nostr"
)

func TestCaptureAcceptsExactSignedPublicMetadata(t *testing.T) {
	key := nostr.Generate()
	self := nostr.GetPublicKey(key)
	event := nostr.Event{Kind: 39000, CreatedAt: nostr.Now(), Tags: nostr.Tags{{"d", "bitcoinwalk-test"}, {"name", "chat"}}}
	if err := event.Sign(key); err != nil {
		t.Fatal(err)
	}
	result, err := summarize([]nostr.Event{event}, "bitcoinwalk-test", self)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Events) != 1 || result.Events[0].ID != event.ID.Hex() || result.EventDigest == "" {
		t.Fatalf("unexpected report: %+v", result)
	}
}
