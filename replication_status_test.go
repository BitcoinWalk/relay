package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.etcd.io/bbolt"
)

func TestReplicaStatusRequiresTokenAndReturnsContentFreeCounts(t *testing.T) {
	registry := &replicaRegistry{destinations: map[string]string{
		cityA: "wss://one.example/",
		cityB: "wss://two.example/",
	}}
	journal, err := openReplicaJournal(filepath.Join(t.TempDir(), "journal.db"), registry, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer journal.Close()
	if err := journal.finishReconciliation(); err != nil {
		t.Fatal(err)
	}
	secretEventID := strings.Repeat("a", 64)
	err = journal.db.Update(func(tx *bbolt.Tx) error {
		if err := putOutboxRow(tx, replicaOutboxRow{EventID: secretEventID, CityID: cityA, Destination: "wss://one.example/", Status: "acknowledged"}); err != nil {
			return err
		}
		return putOutboxRow(tx, replicaOutboxRow{EventID: strings.Repeat("b", 64), CityID: cityB, Destination: "wss://two.example/", Status: "retry", Attempts: 7, LastCode: "delivery-failed"})
	})
	if err != nil {
		t.Fatal(err)
	}

	handler := replicaStatusHandler(journal, []byte(strings.Repeat("1", 64)))
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/replication/status", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("missing token returned %d", unauthorized.Code)
	}

	request := httptest.NewRequest(http.MethodGet, "/replication/status", nil)
	request.Header.Set("Authorization", "Bearer "+strings.Repeat("1", 64))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("authorized status returned %d: %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	for _, forbidden := range []string{secretEventID, "delivery-failed", "attempts", "eventId", "envelope", "bundle"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("status leaked %q: %s", forbidden, body)
		}
	}
	for _, expected := range []string{`"state":"degraded"`, `"cityId":"` + cityA + `"`, `"acknowledged":1`, `"cityId":"` + cityB + `"`, `"retry":1`} {
		if !strings.Contains(body, expected) {
			t.Fatalf("status omitted %s: %s", expected, body)
		}
	}
}

func TestReplicaStatusTokenFileMustBeOwnerOnlyHex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "status-token")
	if err := os.WriteFile(path, []byte(strings.Repeat("a", 64)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if token, err := readReplicaStatusToken(path); err != nil || string(token) != strings.Repeat("a", 64) {
		t.Fatalf("valid token rejected: %q %v", token, err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readReplicaStatusToken(path); err == nil {
		t.Fatal("world-readable status token accepted")
	}
}

func TestReplicaStatusTokenAcceptsSystemdCredentialMetadata(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "status-token")
	if err := os.WriteFile(path, []byte(strings.Repeat("a", 64)), 0640); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CREDENTIALS_DIRECTORY", directory)
	if token, err := readReplicaStatusToken(path); err != nil || string(token) != strings.Repeat("a", 64) {
		t.Fatalf("systemd credential rejected: %q %v", token, err)
	}
}
