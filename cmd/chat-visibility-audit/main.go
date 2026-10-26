// chat-visibility-audit verifies that one NIP-29 group is publicly readable
// while another remains inaccessible to an authenticated outsider. It never
// joins, publishes, or persists its ephemeral audit identity.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"fiatjaf.com/nostr"
)

type report struct {
	PublicGroup   string `json:"publicGroup"`
	PublicEvents  int    `json:"publicEvents"`
	PrivateGroup  string `json:"privateGroup"`
	PrivateDenied bool   `json:"privateDenied"`
}

func publicRead(ctx context.Context, relay *nostr.Relay, group string) (int, error) {
	sub, err := relay.Subscribe(ctx, nostr.Filter{
		Kinds: []nostr.Kind{9}, Tags: nostr.TagMap{"h": {group}}, Limit: 200,
	}, nostr.SubscriptionOptions{})
	if err != nil {
		return 0, err
	}
	defer sub.Unsub()
	count := 0
	for {
		select {
		case <-sub.Events:
			count++
		case reason := <-sub.ClosedReason:
			return 0, fmt.Errorf("public history rejected: %s", reason)
		case <-sub.EndOfStoredEvents:
			return count, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
}

func authenticate(ctx context.Context, relay *nostr.Relay, key nostr.SecretKey) error {
	probe, err := relay.Subscribe(ctx, nostr.Filter{}, nostr.SubscriptionOptions{})
	if err != nil {
		return err
	}
	select {
	case <-probe.ClosedReason:
	case <-ctx.Done():
		probe.Unsub()
		return ctx.Err()
	}
	probe.Unsub()
	return relay.Auth(ctx, func(_ context.Context, event *nostr.Event) error {
		return event.Sign(key)
	})
}

func privateDenied(ctx context.Context, relay *nostr.Relay, group string) error {
	sub, err := relay.Subscribe(ctx, nostr.Filter{
		Kinds: []nostr.Kind{9}, Tags: nostr.TagMap{"h": {group}}, Limit: 1,
	}, nostr.SubscriptionOptions{})
	if err != nil {
		return err
	}
	defer sub.Unsub()
	select {
	case <-sub.Events:
		return errors.New("private message leaked to outsider")
	case <-sub.EndOfStoredEvents:
		return errors.New("private group was readable by outsider")
	case reason := <-sub.ClosedReason:
		if reason == "" {
			return errors.New("private group rejection omitted its reason")
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func run() error {
	relayURL := flag.String("relay", "", "relay URL to inspect")
	publicGroup := flag.String("public-group", "", "public NIP-29 group ID")
	privateGroup := flag.String("private-group", "", "private NIP-29 group ID")
	flag.Parse()
	if *relayURL == "" || *publicGroup == "" || *privateGroup == "" || *publicGroup == *privateGroup {
		return errors.New("distinct -public-group, -private-group and -relay are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	relay, err := nostr.RelayConnect(ctx, *relayURL, nostr.RelayOptions{})
	if err != nil {
		return err
	}
	defer relay.Close()
	count, err := publicRead(ctx, relay, *publicGroup)
	if err != nil {
		return err
	}
	if err := authenticate(ctx, relay, nostr.Generate()); err != nil {
		return err
	}
	if err := privateDenied(ctx, relay, *privateGroup); err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(report{*publicGroup, count, *privateGroup, true})
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
