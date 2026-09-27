package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"fiatjaf.com/nostr"
)

const replicaPolicyAuditConfirmation = "staging-policy-v1"

type replicaPolicyAuditCase struct {
	Name     string `json:"name"`
	Rejected bool   `json:"rejected"`
}

type replicaPolicyAuditResult struct {
	EventID     string                   `json:"sourceEventId"`
	JournalMode string                   `json:"journalMode"`
	Cases       []replicaPolicyAuditCase `json:"cases"`
}

func unauthorizedReplicaAuthorEnvelope(base replicaDeliveryEnvelope, key nostr.SecretKey) (replicaDeliveryEnvelope, error) {
	bundle, err := decodeReplicaBundle(base.Bundle)
	if err != nil {
		return replicaDeliveryEnvelope{}, err
	}
	index := -1
	for i := range bundle.Events {
		if bundle.Events[i].ID.Hex() == bundle.OccurrenceID {
			index = i
			break
		}
	}
	if index < 0 {
		return replicaDeliveryEnvelope{}, errors.New("replica policy audit bundle target is unavailable")
	}
	unauthorized := bundle.Events[index]
	unauthorized.CreatedAt = nostr.Now()
	if err := unauthorized.Sign(key); err != nil {
		return replicaDeliveryEnvelope{}, err
	}
	bundle.Events[index] = unauthorized
	bundle.OccurrenceID = unauthorized.ID.Hex()
	bundleJSON, err := json.Marshal(bundle)
	if err != nil {
		return replicaDeliveryEnvelope{}, err
	}
	test := base
	test.OccurrenceID = unauthorized.ID.Hex()
	test.Event = unauthorized
	test.Bundle = bundleJSON
	return test, nil
}

func expectReplicaPolicyRejection(ctx context.Context, name, expected string, transport replicaTransport, envelope replicaDeliveryEnvelope) (replicaPolicyAuditCase, error) {
	ack, err := transport(ctx, envelope.Destination, envelope)
	if err == nil || ack.Accepted {
		return replicaPolicyAuditCase{}, fmt.Errorf("replica policy audit %s was accepted", name)
	}
	if !strings.Contains(err.Error(), expected) {
		return replicaPolicyAuditCase{}, fmt.Errorf("replica policy audit %s returned an unexpected rejection", name)
	}
	return replicaPolicyAuditCase{Name: name, Rejected: true}, nil
}

func retryRateLimitedReplicaTransport(transport replicaTransport) replicaTransport {
	return func(ctx context.Context, destination string, envelope replicaDeliveryEnvelope) (replicaDeliveryAck, error) {
		for attempt := 0; ; attempt++ {
			ack, err := transport(ctx, destination, envelope)
			if err == nil || !strings.Contains(err.Error(), "429") || attempt >= 5 {
				return ack, err
			}
			select {
			case <-ctx.Done():
				return replicaDeliveryAck{}, ctx.Err()
			case <-time.After(5 * time.Second):
			}
		}
	}
}

func auditReplicaPolicyRejections(ctx context.Context, base replicaDeliveryEnvelope, serviceKey, wrongServiceKey, unauthorizedAuthorKey nostr.SecretKey, authorized, unauthorized replicaTransport) (replicaPolicyAuditResult, error) {
	if nostr.GetPublicKey(serviceKey) == nostr.GetPublicKey(wrongServiceKey) {
		return replicaPolicyAuditResult{}, errors.New("replica policy audit requires a distinct unauthorized service")
	}
	if authorized == nil {
		authorized = newReplicaWebSocketTransport(serviceKey)
	}
	if unauthorized == nil {
		unauthorized = newReplicaWebSocketTransport(wrongServiceKey)
	}
	result := replicaPolicyAuditResult{EventID: base.OccurrenceID, JournalMode: "read-only"}
	wrongService, err := expectReplicaPolicyRejection(ctx, "unauthorized-service", "auth-required:", unauthorized, base)
	if err != nil {
		return replicaPolicyAuditResult{}, err
	}
	result.Cases = append(result.Cases, wrongService)

	foreign := base
	foreign.CityID = "00000000-0000-4000-8000-000000000001"
	foreignCity, err := expectReplicaPolicyRejection(ctx, "foreign-city", "replication wire scope mismatch", authorized, foreign)
	if err != nil {
		return replicaPolicyAuditResult{}, err
	}
	result.Cases = append(result.Cases, foreignCity)

	unauthorizedEnvelope, err := unauthorizedReplicaAuthorEnvelope(base, unauthorizedAuthorKey)
	if err != nil {
		return replicaPolicyAuditResult{}, err
	}
	var bundle replicaBundle
	if err := json.Unmarshal(unauthorizedEnvelope.Bundle, &bundle); err != nil {
		return replicaPolicyAuditResult{}, err
	}
	for _, event := range bundle.Events {
		if event.Kind != 30302 {
			continue
		}
		var grant cityGrant
		if json.Unmarshal([]byte(event.Content), &grant) == nil && slices.Contains(grant.EditorPubkeys, unauthorizedEnvelope.Event.PubKey.Hex()) {
			return replicaPolicyAuditResult{}, errors.New("replica policy audit generated an authorized occurrence author")
		}
	}
	unauthorizedAuthor, err := expectReplicaPolicyRejection(ctx, "unauthorized-author", "occurrence author is not a current city editor", authorized, unauthorizedEnvelope)
	if err != nil {
		return replicaPolicyAuditResult{}, err
	}
	result.Cases = append(result.Cases, unauthorizedAuthor)
	return result, nil
}

func runReplicaPolicyAudit() error {
	if os.Getenv("RELAY_REPLICA_POLICY_AUDIT_CONFIRM") != replicaPolicyAuditConfirmation {
		return errors.New("replica policy audit requires the exact staging confirmation")
	}
	eventID := os.Getenv("RELAY_REPLICA_POLICY_AUDIT_EVENT")
	envelope, err := loadAcknowledgedReplicaEnvelope(os.Getenv("RELAY_REPLICA_JOURNAL"), eventID)
	if err != nil {
		return err
	}
	serviceKey, err := loadReplicaServiceKey(os.Getenv("RELAY_REPLICA_DELIVERY_KEY_FILE"))
	if err != nil {
		return fmt.Errorf("load replica policy audit key: %w", err)
	}
	wrongServiceKey := nostr.Generate()
	result, err := auditReplicaPolicyRejections(
		context.Background(),
		*envelope,
		serviceKey,
		wrongServiceKey,
		nostr.Generate(),
		retryRateLimitedReplicaTransport(newReplicaWebSocketTransport(serviceKey)),
		retryRateLimitedReplicaTransport(newReplicaWebSocketTransport(wrongServiceKey)),
	)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(result)
}
