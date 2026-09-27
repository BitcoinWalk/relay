package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore"
	"fiatjaf.com/nostr/khatru"
)

const (
	replicaTransportKind    nostr.Kind = 6000
	replicaWireContentLimit            = 400 * 1024
	replicaWireMessageLimit            = 512 * 1024
	replicaTransportTimeout            = 5 * time.Second
	replicaDeliveryInterval            = 5 * time.Second
)

type replicaRelayConnector func(context.Context, string, nostr.RelayOptions) (*nostr.Relay, error)

func replicaWireTags(envelope replicaDeliveryEnvelope) nostr.Tags {
	return nostr.Tags{
		{"d", "bitcoinwalk-replication-v1"},
		{"i", envelope.CityID},
		{"destination", envelope.Destination},
		{"action", replicaEnvelopeAction(envelope)},
		{"e", replicaEnvelopeAckID(envelope), "", "replica-delivery"},
		{"sequence", strconv.FormatUint(envelope.SourceSequence, 10)},
	}
}

func exactReplicaWireTags(actual nostr.Tags, envelope replicaDeliveryEnvelope) bool {
	expected := replicaWireTags(envelope)
	if len(actual) != len(expected) {
		return false
	}
	for i := range expected {
		if len(actual[i]) != len(expected[i]) {
			return false
		}
		for j := range expected[i] {
			if actual[i][j] != expected[i][j] {
				return false
			}
		}
	}
	return true
}

func newReplicaWireEvent(envelope replicaDeliveryEnvelope, serviceKey nostr.SecretKey) (nostr.Event, error) {
	content, err := json.Marshal(envelope)
	if err != nil {
		return nostr.Event{}, err
	}
	if len(content) == 0 || len(content) > replicaWireContentLimit {
		return nostr.Event{}, errors.New("replica wire envelope exceeds the bounded content limit")
	}
	event := nostr.Event{
		CreatedAt: nostr.Now(),
		Kind:      replicaTransportKind,
		Tags:      replicaWireTags(envelope),
		Content:   string(content),
	}
	if err := event.Sign(serviceKey); err != nil {
		return nostr.Event{}, err
	}
	wire, err := (nostr.EventEnvelope{Event: event}).MarshalJSON()
	if err != nil || len(wire) > replicaWireMessageLimit {
		return nostr.Event{}, errors.New("replica wire event exceeds the WebSocket message limit")
	}
	return event, nil
}

func decodeReplicaWireEvent(event nostr.Event, scope replicaScope) (*replicaDeliveryEnvelope, error) {
	if event.Kind != replicaTransportKind || event.PubKey != scope.ServiceKey || !event.CheckID() || !event.VerifySignature() {
		return nil, errors.New("restricted: invalid replication service event")
	}
	if event.CreatedAt < nostr.Now()-60 || event.CreatedAt > nostr.Now()+60 || len(event.Content) == 0 || len(event.Content) > replicaWireContentLimit {
		return nil, errors.New("restricted: stale or oversized replication service event")
	}
	decoder := json.NewDecoder(strings.NewReader(event.Content))
	decoder.DisallowUnknownFields()
	var envelope replicaDeliveryEnvelope
	if err := decoder.Decode(&envelope); err != nil {
		return nil, errors.New("invalid: replication wire envelope")
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("invalid: trailing replication wire data")
	}
	if !exactReplicaWireTags(event.Tags, envelope) || envelope.CityID != scope.CityID || envelope.Destination != scope.Destination {
		return nil, errors.New("restricted: replication wire scope mismatch")
	}
	return &envelope, nil
}

// attachReplicaReceiverTransport installs an opt-in, non-persisting Nostr EVENT
// transport. The wrapper event is service-signed and NIP-42-authenticated. The
// embedded organizer/admin events retain their own original signatures.
func attachReplicaReceiverTransport(relay *khatru.Relay, receiver *replicaReceiver) error {
	if relay == nil || receiver == nil {
		return errors.New("replica receiver transport requires relay and receiver")
	}
	previousOnEvent := relay.OnEvent
	previousStoreEvent := relay.StoreEvent
	relay.OnEvent = func(ctx context.Context, event nostr.Event) (bool, string) {
		if event.Kind != replicaTransportKind {
			if previousOnEvent == nil {
				return false, ""
			}
			return previousOnEvent(ctx, event)
		}
		if !khatru.IsAuthed(ctx, receiver.scope.ServiceKey) {
			return true, "auth-required: authenticate as configured replication service"
		}
		envelope, err := decodeReplicaWireEvent(event, receiver.scope)
		if err != nil {
			return true, err.Error()
		}
		ack, err := receiver.receiveReplicaEnvelope(ctx, *envelope)
		if err != nil {
			return true, err.Error()
		}
		if !ack.Accepted || ack.EventID != replicaEnvelopeAckID(*envelope) || ack.SourceSequence != envelope.SourceSequence {
			return true, "error: replica receiver returned an inexact acknowledgement"
		}
		return false, ""
	}
	relay.StoreEvent = func(ctx context.Context, event nostr.Event) error {
		if event.Kind == replicaTransportKind {
			// Khatru treats a duplicate as successfully handled while suppressing
			// storage, OnEventSaved and broadcast of the private wire wrapper.
			return eventstore.ErrDupEvent
		}
		if previousStoreEvent == nil {
			return errors.New("relay event storage unavailable")
		}
		return previousStoreEvent(ctx, event)
	}
	if relay.MaxMessageSize < replicaWireMessageLimit {
		relay.MaxMessageSize = replicaWireMessageLimit
	}
	return nil
}

func deliverReplicaEnvelopeWSS(ctx context.Context, destination string, envelope replicaDeliveryEnvelope, serviceKey nostr.SecretKey, connect replicaRelayConnector) (replicaDeliveryAck, error) {
	normalized, err := normalizeReplicaDestination(destination)
	if err != nil || normalized != destination || envelope.Destination != destination {
		return replicaDeliveryAck{}, errors.New("restricted: invalid replica transport destination")
	}
	if connect == nil {
		connect = nostr.RelayConnect
	}
	wireEvent, err := newReplicaWireEvent(envelope, serviceKey)
	if err != nil {
		return replicaDeliveryAck{}, err
	}
	attemptCtx, cancel := context.WithTimeout(ctx, replicaTransportTimeout)
	defer cancel()
	client, err := connect(attemptCtx, destination, nostr.RelayOptions{})
	if err != nil {
		return replicaDeliveryAck{}, fmt.Errorf("replica websocket connect failed: %w", err)
	}
	defer client.Close()
	// Khatru issues its NIP-42 challenge together with the first auth-required
	// rejection. The rejected probe cannot reach receiver state.
	if err := client.Publish(attemptCtx, wireEvent); err == nil {
		return replicaDeliveryAck{}, errors.New("replica websocket receiver did not require authentication")
	} else if !strings.Contains(err.Error(), "auth-required:") {
		return replicaDeliveryAck{}, fmt.Errorf("replica websocket authentication challenge failed: %w", err)
	}
	if err := client.Auth(attemptCtx, func(_ context.Context, event *nostr.Event) error { return event.Sign(serviceKey) }); err != nil {
		return replicaDeliveryAck{}, fmt.Errorf("replica websocket authentication failed: %w", err)
	}
	if err := client.Publish(attemptCtx, wireEvent); err != nil {
		return replicaDeliveryAck{}, fmt.Errorf("replica websocket delivery failed: %w", err)
	}
	return replicaDeliveryAck{EventID: replicaEnvelopeAckID(envelope), SourceSequence: envelope.SourceSequence, Accepted: true}, nil
}

func newReplicaWebSocketTransport(serviceKey nostr.SecretKey) replicaTransport {
	return func(ctx context.Context, destination string, envelope replicaDeliveryEnvelope) (replicaDeliveryAck, error) {
		return deliverReplicaEnvelopeWSS(ctx, destination, envelope, serviceKey, nil)
	}
}

func loadReplicaServiceKey(path string) (nostr.SecretKey, error) {
	return readRelayCredential(path)
}

func initializeReplicaServiceKey(path string) (nostr.PubKey, error) {
	var zero nostr.PubKey
	if err := createRelayCredential(path); err != nil {
		return zero, err
	}
	key, err := readRelayCredential(path)
	if err != nil {
		return zero, err
	}
	return nostr.GetPublicKey(key), nil
}

func runReplicaDeliveryWorker(ctx context.Context, policy *organizerPolicy, journal *replicaJournal, transport replicaTransport) {
	ticker := time.NewTicker(replicaDeliveryInterval)
	defer ticker.Stop()
	for {
		delivered, err := policy.deliverReplicaOutbox(ctx, journal, time.Now(), transport)
		if err != nil && ctx.Err() == nil {
			journal.setUnhealthy(err)
		}
		if delivered > 0 {
			// Deliberately no event IDs, content or remote diagnostics in logs.
			fmt.Fprintf(os.Stderr, "replica delivery acknowledged %d queued item(s)\n", delivered)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
