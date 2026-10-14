// chat-public-audit captures the exact public NIP-29 metadata surface for one
// group. It is read-only: it does not authenticate, sign, join or publish.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"time"

	"fiatjaf.com/nostr"
)

type row struct {
	Kind int    `json:"kind"`
	ID   string `json:"id"`
}

type report struct {
	GroupID     string `json:"groupId"`
	RelaySelf   string `json:"relaySelf"`
	EventDigest string `json:"eventDigest"`
	Events      []row  `json:"events"`
}

func capture(ctx context.Context, relayURL, groupID string, relaySelf nostr.PubKey) (report, error) {
	relay, err := nostr.RelayConnect(ctx, relayURL, nostr.RelayOptions{})
	if err != nil {
		return report{}, err
	}
	defer relay.Close()
	rows := make([]nostr.Event, 0, 4)
	for event := range relay.QueryEvents(nostr.Filter{Kinds: []nostr.Kind{39000, 39001, 39002, 39003}, Tags: nostr.TagMap{"d": {groupID}}, Limit: 20}) {
		rows = append(rows, event)
	}
	return summarize(rows, groupID, relaySelf)
}

func summarize(source []nostr.Event, groupID string, relaySelf nostr.PubKey) (report, error) {
	events := make([]row, 0, 4)
	seen := map[int]bool{}
	for _, event := range source {
		if event.PubKey != relaySelf || !event.CheckID() || !event.VerifySignature() {
			return report{}, errors.New("metadata identity or signature is invalid")
		}
		exactD := 0
		for _, tag := range event.Tags {
			if len(tag) == 2 && tag[0] == "d" && tag[1] == groupID {
				exactD++
			}
		}
		if exactD != 1 || seen[int(event.Kind)] {
			return report{}, errors.New("metadata set is ambiguous")
		}
		seen[int(event.Kind)] = true
		events = append(events, row{int(event.Kind), event.ID.Hex()})
	}
	if !seen[39000] {
		return report{}, errors.New("public group metadata is missing")
	}
	sort.Slice(events, func(i, j int) bool { return events[i].Kind < events[j].Kind })
	hash := sha256.New()
	for _, event := range events {
		fmt.Fprintf(hash, "%d:%s\n", event.Kind, event.ID)
	}
	return report{groupID, relaySelf.Hex(), hex.EncodeToString(hash.Sum(nil)), events}, nil
}

func run() error {
	relayURL := flag.String("relay", "", "relay URL to inspect")
	groupID := flag.String("group", "", "exact NIP-29 group ID")
	selfHex := flag.String("relay-self", "", "expected relay public key")
	flag.Parse()
	if *relayURL == "" || *groupID == "" || *selfHex == "" {
		return errors.New("-relay, -group and -relay-self are required")
	}
	self, err := nostr.PubKeyFromHex(*selfHex)
	if err != nil {
		return errors.New("invalid expected relay identity")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	result, err := capture(ctx, *relayURL, *groupID, self)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
