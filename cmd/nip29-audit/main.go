// nip29-audit is read-only. It neither authenticates, signs nor publishes events.
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
	"github.com/BitcoinWalk/relay/internal/citygroups"
)

func run() error {
	relayURL := flag.String("relay", "wss://relay-staging.bitcoinwalk.org", "relay to audit without writing")
	flag.Parse()
	admin := nostr.MustPubKeyFromHex("90cf043861e5b5a9972cb7b529a5ba71b215d6d1e314c749d5526ec133f1db73")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	relay, err := nostr.RelayConnect(ctx, *relayURL, nostr.RelayOptions{})
	if err != nil {
		return err
	}
	defer relay.Close()
	sub, err := relay.Subscribe(ctx, nostr.Filter{Kinds: []nostr.Kind{30302}, Authors: []nostr.PubKey{admin}, Limit: 200}, nostr.SubscriptionOptions{})
	if err != nil {
		return err
	}
	defer sub.Unsub()
	events := []nostr.Event{}
collect:
	for {
		select {
		case event, ok := <-sub.Events:
			if !ok {
				return errors.New("subscription ended before EOSE")
			}
			events = append(events, event)
		case eose := <-sub.EndOfStoredEvents:
			for _, hint := range eose.Hint {
				if hint == "more" {
					return errors.New("relay reports incomplete results; paginated audit required")
				}
			}
			break collect
		case reason := <-sub.ClosedReason:
			return fmt.Errorf("relay rejected audit: %s", reason)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if len(events) >= 200 {
		return errors.New("query limit reached; refusing to treat partial results as a full migration")
	}
	seeds, err := citygroups.SelectSeeds(events, admin)
	if err != nil {
		return err
	}
	type row struct {
		CityID          string `json:"cityId"`
		SourceID        string `json:"sourceAuthorizationId"`
		Editors         int    `json:"editorCount"`
		CreatorRetained bool   `json:"creatorRetained"`
	}
	rows := make([]row, 0, len(seeds))
	for _, seed := range seeds {
		rows = append(rows, row{seed.CityID, seed.SourceID.Hex(), len(seed.Editors), true})
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(struct {
		Relay    string `json:"relay"`
		ReadOnly bool   `json:"readOnly"`
		Groups   []row  `json:"groups"`
		Note     string `json:"note"`
	}{*relayURL, true, rows, "No keys generated, no metadata published, no permissions changed. Relay storage completeness cannot be proven remotely."})
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
