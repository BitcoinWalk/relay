package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"fiatjaf.com/nostr"
)

func TestReplicaPolicyAuditRejectsBeforeAcceptanceAndLeavesJournalReadOnly(t *testing.T) {
	path, base := replayJournalFixture(t, "acknowledged")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	service, wrongService, unauthorizedAuthor := nostr.Generate(), nostr.Generate(), nostr.Generate()
	base, err = loadPolicyAuditEnvelopeFixture(base, nostr.Generate())
	if err != nil {
		t.Fatal(err)
	}
	calls := []string{}
	authorized := func(_ context.Context, _ string, envelope replicaDeliveryEnvelope) (replicaDeliveryAck, error) {
		if envelope.CityID != base.CityID {
			calls = append(calls, "foreign-city")
			return replicaDeliveryAck{}, errors.New("restricted: replication wire scope mismatch")
		}
		if envelope.Event.PubKey == nostr.GetPublicKey(unauthorizedAuthor) {
			calls = append(calls, "unauthorized-author")
			return replicaDeliveryAck{}, errors.New("restricted: occurrence author is not a current city editor")
		}
		t.Fatal("authorized transport received an unexpected envelope")
		return replicaDeliveryAck{}, nil
	}
	unauthorized := func(context.Context, string, replicaDeliveryEnvelope) (replicaDeliveryAck, error) {
		calls = append(calls, "unauthorized-service")
		return replicaDeliveryAck{}, errors.New("auth-required: authenticate as configured replication service")
	}
	result, err := auditReplicaPolicyRejections(t.Context(), base, service, wrongService, unauthorizedAuthor, authorized, unauthorized)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(calls, ",") != "unauthorized-service,foreign-city,unauthorized-author" || len(result.Cases) != 3 {
		t.Fatalf("unexpected policy audit: calls=%v result=%#v", calls, result)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("policy audit changed the source journal")
	}
}

func loadPolicyAuditEnvelopeFixture(base replicaDeliveryEnvelope, authorized nostr.SecretKey) (replicaDeliveryEnvelope, error) {
	revision := nostr.Event{Kind: 30303, Content: `{}`}
	revision.Sign(authorized)
	grant := cityGrant{CityID: base.CityID, CreatorPubkey: authorized.Public().Hex(), CreatorRevisionID: revision.ID.Hex(), EditorPubkeys: []string{authorized.Public().Hex()}, SuperAdminPubkey: nostr.Generate().Public().Hex()}
	grantEvent := nostr.Event{Kind: 30302, Content: mustJSON(grant)}
	grantEvent.Sign(nostr.Generate())
	target := base.Event
	target.Sign(authorized)
	bundle := replicaBundle{Version: 1, CityID: base.CityID, Events: []nostr.Event{revision, grantEvent, target}, OccurrenceID: target.ID.Hex()}
	data, err := json.Marshal(bundle)
	if err != nil {
		return replicaDeliveryEnvelope{}, err
	}
	base.Event = target
	base.OccurrenceID = target.ID.Hex()
	base.Bundle = data
	return base, nil
}

func mustJSON(value any) string {
	data, _ := json.Marshal(value)
	return string(data)
}
