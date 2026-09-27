// replica-audit is read-only. It neither authenticates, signs nor publishes.
package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"fiatjaf.com/nostr"
)

type relayResult struct {
	URL                string   `json:"url"`
	OccurrenceIDs      []string `json:"occurrenceIds"`
	AllSignaturesValid bool     `json:"allSignaturesValid"`
	PrivateWrappers    int      `json:"privateWrapperCount"`
}

type auditReport struct {
	CityID        string      `json:"cityId"`
	ReadOnly      bool        `json:"readOnly"`
	Source        relayResult `json:"source"`
	Replica       relayResult `json:"replica"`
	ExactEventIDs bool        `json:"exactEventIds"`
	AllowEmpty    bool        `json:"allowEmpty"`
	Baseline      bool        `json:"baseline,omitempty"`
}

func validOccurrenceIDs(ids []string) bool {
	if !slices.IsSorted(ids) {
		return false
	}
	previous := ""
	for _, id := range ids {
		decoded, err := hex.DecodeString(id)
		if err != nil || len(id) != 64 || len(decoded) != 32 || strings.ToLower(id) != id || id == previous {
			return false
		}
		previous = id
	}
	return true
}

func trustedRelayResult(result relayResult) bool {
	return result.URL != "" && result.AllSignaturesValid && result.PrivateWrappers == 0 && validOccurrenceIDs(result.OccurrenceIDs)
}

func loadAuditBaseline(path, cityID string, allowEmpty bool) (auditReport, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0022 != 0 || info.Size() > 64*1024 {
		return auditReport{}, errors.New("baseline must be a bounded non-writable regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return auditReport{}, err
	}
	var report auditReport
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&report); err != nil {
		return auditReport{}, fmt.Errorf("decode baseline: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return auditReport{}, errors.New("invalid trailing baseline data")
	}
	if report.CityID != cityID || !report.ReadOnly || !report.ExactEventIDs || !trustedRelayResult(report.Source) || !trustedRelayResult(report.Replica) || !slices.Equal(report.Source.OccurrenceIDs, report.Replica.OccurrenceIDs) {
		return auditReport{}, errors.New("baseline is not an exact trusted audit for this city")
	}
	if len(report.Source.OccurrenceIDs) == 0 && (!allowEmpty || !report.AllowEmpty) {
		return auditReport{}, errors.New("empty baseline requires explicit allowance")
	}
	return report, nil
}

func query(ctx context.Context, relayURL string, filter nostr.Filter) ([]nostr.Event, error) {
	relay, err := nostr.RelayConnect(ctx, relayURL, nostr.RelayOptions{})
	if err != nil {
		return nil, err
	}
	defer relay.Close()
	sub, err := relay.Subscribe(ctx, filter, nostr.SubscriptionOptions{})
	if err != nil {
		return nil, err
	}
	defer sub.Unsub()
	events := make([]nostr.Event, 0)
	for {
		select {
		case event, ok := <-sub.Events:
			if !ok {
				return nil, errors.New("subscription ended before EOSE")
			}
			events = append(events, event)
		case eose := <-sub.EndOfStoredEvents:
			for _, hint := range eose.Hint {
				if hint == "more" {
					return nil, errors.New("relay reports incomplete results")
				}
			}
			return events, nil
		case reason := <-sub.ClosedReason:
			return nil, fmt.Errorf("relay closed audit query: %s", reason)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func auditRelay(ctx context.Context, relayURL, cityID string) (relayResult, error) {
	events, err := query(ctx, relayURL, nostr.Filter{Kinds: []nostr.Kind{31923}, Tags: nostr.TagMap{"i": {cityID}}, Limit: 200})
	if err != nil {
		return relayResult{}, err
	}
	if len(events) >= 200 {
		return relayResult{}, errors.New("occurrence query reached its safety limit")
	}
	result := relayResult{URL: relayURL, AllSignaturesValid: true, OccurrenceIDs: make([]string, 0, len(events))}
	for _, event := range events {
		result.OccurrenceIDs = append(result.OccurrenceIDs, event.ID.Hex())
		result.AllSignaturesValid = result.AllSignaturesValid && event.CheckID() && event.VerifySignature()
	}
	slices.Sort(result.OccurrenceIDs)
	wrappers, err := query(ctx, relayURL, nostr.Filter{Kinds: []nostr.Kind{6000}, Limit: 1})
	if err != nil {
		return relayResult{}, err
	}
	result.PrivateWrappers = len(wrappers)
	return result, nil
}

func run() error {
	sourceURL := flag.String("source", "wss://relay-staging.bitcoinwalk.org", "source relay URL")
	replicaURL := flag.String("replica", "wss://replica-staging.bitcoinwalk.org", "replica relay URL")
	cityID := flag.String("city", "be8514a4-9df0-4159-a517-71f65761cbbe", "exact city UUID")
	allowEmpty := flag.Bool("allow-empty", false, "accept exact zero-event equality (pre-release rehearsal only)")
	baselinePath := flag.String("baseline", "", "trusted exact audit captured before a deliberate source outage")
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var source relayResult
	if *baselinePath != "" {
		baseline, err := loadAuditBaseline(*baselinePath, *cityID, *allowEmpty)
		if err != nil {
			return err
		}
		source = baseline.Source
	} else {
		var err error
		source, err = auditRelay(ctx, *sourceURL, *cityID)
		if err != nil {
			return fmt.Errorf("source audit: %w", err)
		}
	}
	replica, err := auditRelay(ctx, *replicaURL, *cityID)
	if err != nil {
		return fmt.Errorf("replica audit: %w", err)
	}
	exact := slices.Equal(source.OccurrenceIDs, replica.OccurrenceIDs)
	result := auditReport{CityID: *cityID, ReadOnly: true, Source: source, Replica: replica, ExactEventIDs: exact, AllowEmpty: *allowEmpty, Baseline: *baselinePath != ""}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		return err
	}
	if len(source.OccurrenceIDs) == 0 && !*allowEmpty || !exact || !source.AllSignaturesValid || !replica.AllSignaturesValid || source.PrivateWrappers != 0 || replica.PrivateWrappers != 0 {
		return errors.New("replication acceptance failed")
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
