// combined-chat-probe performs a bounded, non-destructive acceptance check of
// the NIP-29 surface on a combined city event/community relay.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"fiatjaf.com/nostr"
)

type report struct {
	Relay                    string `json:"relay"`
	ReadOnly                 bool   `json:"readOnly"`
	MetadataCount            int    `json:"metadataCount"`
	AuthenticationAccepted   bool   `json:"authenticationAccepted"`
	UnauthorizedCreateDenied bool   `json:"unauthorizedCreateDenied"`
}

func run() error {
	relayURL := flag.String("relay", "", "combined relay URL")
	authRelayURL := flag.String("auth-relay", "", "canonical relay URL used in the NIP-42 proof")
	flag.Parse()
	if *relayURL == "" {
		return errors.New("-relay is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := nostr.RelayConnect(ctx, *relayURL, nostr.RelayOptions{})
	if err != nil {
		return err
	}
	defer client.Close()

	metadata := make([]nostr.Event, 0)
	for event := range client.QueryEvents(nostr.Filter{Kinds: []nostr.Kind{39000}, Limit: 20}) {
		metadata = append(metadata, event)
	}
	if len(metadata) != 0 {
		return errors.New("rehearsal relay unexpectedly contains NIP-29 groups")
	}

	key := nostr.Generate()
	probe, err := client.Subscribe(ctx, nostr.Filter{Kinds: []nostr.Kind{9}, Tags: nostr.TagMap{"h": {"bitcoinwalk-rehearsal-denied"}}}, nostr.SubscriptionOptions{})
	if err != nil {
		return err
	}
	select {
	case reason := <-probe.ClosedReason:
		if !strings.Contains(reason, "auth-required") {
			probe.Unsub()
			return fmt.Errorf("unexpected authentication challenge: %s", reason)
		}
	case <-ctx.Done():
		probe.Unsub()
		return errors.New("combined relay did not require chat authentication")
	}
	probe.Unsub()
	if *authRelayURL != "" {
		// Rehearsals connect over an isolated Docker address while the relay
		// deliberately validates authentication against its canonical origin.
		// Changing this field affects the proof tag, not the open WebSocket.
		client.URL = nostr.NormalizeURL(*authRelayURL)
	}
	if err := client.Auth(ctx, func(_ context.Context, event *nostr.Event) error { return event.Sign(key) }); err != nil {
		return fmt.Errorf("authenticate rehearsal identity: %w", err)
	}
	create := nostr.Event{
		Kind:      9007,
		CreatedAt: nostr.Now(),
		Tags:      nostr.Tags{{"h", "bitcoinwalk-rehearsal-denied"}},
		Content:   "rehearsal-only unauthorized group creation",
	}
	if err := create.Sign(key); err != nil {
		return err
	}
	publishErr := client.Publish(ctx, create)
	if publishErr == nil || !strings.Contains(publishErr.Error(), "restricted") {
		return fmt.Errorf("unauthorized group creation was not rejected: %v", publishErr)
	}

	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report{
		Relay:                    *relayURL,
		ReadOnly:                 true,
		MetadataCount:            0,
		AuthenticationAccepted:   true,
		UnauthorizedCreateDenied: true,
	})
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
