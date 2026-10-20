package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"fiatjaf.com/nostr"
)

const giftWrapKind nostr.Kind = 1059

type report struct {
	Relay                  string `json:"relay"`
	EventID                string `json:"eventId"`
	UnauthenticatedBlocked bool   `json:"unauthenticatedBlocked"`
	ForeignReaderBlocked   bool   `json:"foreignReaderBlocked"`
	RecipientReadExact     bool   `json:"recipientReadExact"`
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func signWith(secret nostr.SecretKey) func(context.Context, *nostr.Event) error {
	return func(_ context.Context, event *nostr.Event) error { return event.Sign(secret) }
}

func waitClosed(ctx context.Context, subscription *nostr.Subscription, prefix string) error {
	select {
	case event := <-subscription.Events:
		return fmt.Errorf("relay disclosed event %s instead of closing request", event.ID.Hex())
	case reason := <-subscription.ClosedReason:
		if !strings.HasPrefix(reason, prefix) {
			return fmt.Errorf("unexpected CLOSED reason %q", reason)
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func challengeAndAuth(ctx context.Context, relay *nostr.Relay, filter nostr.Filter, secret nostr.SecretKey) error {
	subscription, err := relay.Subscribe(ctx, filter, nostr.SubscriptionOptions{})
	if err != nil {
		return err
	}
	defer subscription.Unsub()
	if err := waitClosed(ctx, subscription, "auth-required:"); err != nil {
		return err
	}
	return relay.Auth(ctx, signWith(secret))
}

func main() {
	relayURL := flag.String("relay", "wss://inbox-staging.bitcoinwalk.org/", "NIP-17 inbox relay URL")
	timeout := flag.Duration("timeout", 20*time.Second, "overall audit timeout")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	recipientSecret := nostr.Generate()
	recipientKey := nostr.GetPublicKey(recipientSecret)
	filter := nostr.Filter{
		Kinds: []nostr.Kind{giftWrapKind},
		Tags:  nostr.TagMap{"p": {recipientKey.Hex()}},
		Limit: 20,
	}

	senderSecret := nostr.Generate()
	sender, err := nostr.RelayConnect(ctx, *relayURL, nostr.RelayOptions{})
	if err != nil {
		fail("connect sender: %v", err)
	}
	defer sender.Close()
	event := nostr.Event{
		Kind:      giftWrapKind,
		CreatedAt: nostr.Timestamp(time.Now().Add(-time.Hour).Unix()),
		Tags: nostr.Tags{
			{"p", recipientKey.Hex()},
			{"expiration", strconv.FormatInt(time.Now().Add(5*time.Minute).Unix(), 10)},
		},
		Content: "bitcoinwalk-inbox-audit-" + strconv.FormatInt(time.Now().UnixNano(), 10),
	}
	if err := event.Sign(nostr.Generate()); err != nil {
		fail("sign gift wrap: %v", err)
	}
	unauthenticatedBlocked := false
	if err := sender.Publish(ctx, event); err == nil {
		fail("unauthenticated publish unexpectedly succeeded")
	} else if strings.Contains(err.Error(), "auth-required:") {
		unauthenticatedBlocked = true
	} else {
		fail("unexpected unauthenticated publish result: %v", err)
	}
	if err := sender.Auth(ctx, signWith(senderSecret)); err != nil {
		fail("authenticate sender: %v", err)
	}
	if err := sender.Publish(ctx, event); err != nil {
		fail("authenticated publish: %v", err)
	}

	outsiderSecret := nostr.Generate()
	outsider, err := nostr.RelayConnect(ctx, *relayURL, nostr.RelayOptions{})
	if err != nil {
		fail("connect foreign reader: %v", err)
	}
	defer outsider.Close()
	if err := challengeAndAuth(ctx, outsider, filter, outsiderSecret); err != nil {
		fail("authenticate foreign reader: %v", err)
	}
	foreignRequest, err := outsider.Subscribe(ctx, filter, nostr.SubscriptionOptions{})
	if err != nil {
		fail("foreign reader request: %v", err)
	}
	if err := waitClosed(ctx, foreignRequest, "restricted:"); err != nil {
		fail("foreign reader isolation: %v", err)
	}
	foreignRequest.Unsub()

	recipient, err := nostr.RelayConnect(ctx, *relayURL, nostr.RelayOptions{})
	if err != nil {
		fail("connect recipient: %v", err)
	}
	defer recipient.Close()
	if err := challengeAndAuth(ctx, recipient, filter, recipientSecret); err != nil {
		fail("authenticate recipient: %v", err)
	}
	inbox, err := recipient.Subscribe(ctx, filter, nostr.SubscriptionOptions{})
	if err != nil {
		fail("recipient inbox request: %v", err)
	}
	defer inbox.Unsub()
	exact := false
	for !exact {
		select {
		case got := <-inbox.Events:
			exact = got.ID == event.ID
		case reason := <-inbox.ClosedReason:
			fail("recipient inbox closed: %s", reason)
		case <-ctx.Done():
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				fail("recipient did not receive the exact audit gift wrap before timeout")
			}
			fail("recipient read: %v", ctx.Err())
		}
	}

	result := report{
		Relay:                  *relayURL,
		EventID:                event.ID.Hex(),
		UnauthenticatedBlocked: unauthenticatedBlocked,
		ForeignReaderBlocked:   true,
		RecipientReadExact:     true,
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		fail("encode report: %v", err)
	}
}
