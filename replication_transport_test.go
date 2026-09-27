package main

import (
	"os"
	"path/filepath"
	"testing"

	"fiatjaf.com/nostr"
)

func TestInitializeReplicaServiceKeyDoesNotExposeOrOverwriteSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "replica-key")
	pubkey, err := initializeReplicaServiceKey(path)
	if err != nil {
		t.Fatal(err)
	}
	key, err := loadReplicaServiceKey(path)
	if err != nil {
		t.Fatal(err)
	}
	if pubkey != nostr.GetPublicKey(key) || pubkey.Hex() == key.Hex() {
		t.Fatal("replication key initializer returned the wrong public identity")
	}
	if _, err := initializeReplicaServiceKey(path); err == nil {
		t.Fatal("replication key initializer overwrote an existing secret")
	}
	retained, err := loadReplicaServiceKey(path)
	if err != nil || retained != key {
		t.Fatal("failed reinitialization changed the existing secret")
	}
}

func TestReplicaTransportWireAndKeyFileAreStrict(t *testing.T) {
	service := nostr.Generate()
	dir := t.TempDir()
	path := filepath.Join(dir, "replica.key")
	if err := os.WriteFile(path, []byte(service.Hex()+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadReplicaServiceKey(path)
	if err != nil || loaded != service {
		t.Fatalf("owner-only service key did not load: %v", err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadReplicaServiceKey(path); err == nil {
		t.Fatal("world-readable replication key was accepted")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "replica-link.key")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := loadReplicaServiceKey(link); err == nil {
		t.Fatal("replication key symlink was accepted")
	}
	if err := os.WriteFile(path, []byte("01\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadReplicaServiceKey(path); err == nil {
		t.Fatal("short replication key was accepted")
	}

	target := nostr.Event{Kind: 31923, CreatedAt: nostr.Now(), Tags: nostr.Tags{{"i", cityA}}, Content: "wire target"}
	target.Sign(nostr.Generate())
	envelope := replicaDeliveryEnvelope{Version: replicaEnvelopeVersion, Action: "occurrence", CityID: cityA, Destination: "wss://madeira.example/", OccurrenceID: target.ID.Hex(), SourceSequence: 7, Event: target}
	wire, err := newReplicaWireEvent(envelope, service)
	if err != nil {
		t.Fatal(err)
	}
	scope := replicaScope{CityID: cityA, Destination: envelope.Destination, ServiceKey: service.Public()}
	decoded, err := decodeReplicaWireEvent(wire, scope)
	if err != nil || decoded.OccurrenceID != envelope.OccurrenceID || decoded.SourceSequence != envelope.SourceSequence {
		t.Fatalf("signed wire envelope did not round-trip: %#v %v", decoded, err)
	}
	tampered := wire
	tampered.Tags = append(nostr.Tags(nil), wire.Tags...)
	tampered.Tags[1] = nostr.Tag{"i", cityB}
	if _, err := decodeReplicaWireEvent(tampered, scope); err == nil {
		t.Fatal("tampered wire scope was accepted")
	}
	oversized := envelope
	oversized.Bundle = make([]byte, replicaWireContentLimit+1)
	if _, err := newReplicaWireEvent(oversized, service); err == nil {
		t.Fatal("oversized wire envelope was accepted")
	}
}
