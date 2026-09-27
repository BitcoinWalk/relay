package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeRegistryFixture(t *testing.T, path string, cities []replicaRegistryCity) {
	t.Helper()
	data, err := json.Marshal(replicaRegistryFile{Version: replicaRegistryVersion, Cities: cities})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func registryAddFixture(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	journalPath := filepath.Join(dir, "journal.db")
	currentPath := filepath.Join(dir, "current.json")
	writeRegistryFixture(t, currentPath, []replicaRegistryCity{{CityID: cityA, Destination: "wss://one.example/"}})
	current, err := loadReplicaRegistry(currentPath)
	if err != nil {
		t.Fatal(err)
	}
	journal, err := openReplicaJournal(journalPath, current, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	return journalPath, currentPath
}

func TestReplicaRegistryAddIsExplicitAndAdditive(t *testing.T) {
	journalPath, currentPath := registryAddFixture(t)
	candidatePath := filepath.Join(filepath.Dir(currentPath), "candidate.json")
	writeRegistryFixture(t, candidatePath, []replicaRegistryCity{
		{CityID: cityA, Destination: "wss://one.example/"},
		{CityID: cityB, Destination: "wss://two.example/"},
	})
	result, err := addReplicaRegistryCity(journalPath, currentPath, candidatePath, replicaRegistryAddConfirmation)
	if err != nil || result.CityID != cityB || result.Destination != "wss://two.example/" || result.PreviousDigest == result.CurrentDigest {
		t.Fatalf("explicit registry add failed: %#v %v", result, err)
	}
	candidate, err := loadReplicaRegistry(candidatePath)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := openReplicaJournal(journalPath, candidate, nil)
	if err != nil {
		t.Fatalf("journal rejected the explicitly added registry: %v", err)
	}
	reopened.Close()
}

func TestReplicaRegistryAddRejectsRedirectRemovalAndBroadChange(t *testing.T) {
	for _, test := range []struct {
		name   string
		cities []replicaRegistryCity
	}{
		{"redirect", []replicaRegistryCity{{CityID: cityA, Destination: "wss://changed.example/"}, {CityID: cityB, Destination: "wss://two.example/"}}},
		{"removal", []replicaRegistryCity{{CityID: cityB, Destination: "wss://two.example/"}}},
		{"two additions", []replicaRegistryCity{{CityID: cityA, Destination: "wss://one.example/"}, {CityID: cityB, Destination: "wss://two.example/"}, {CityID: "88f137cb-2ac1-4eef-8358-7dd66b45922f", Destination: "wss://three.example/"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			journalPath, currentPath := registryAddFixture(t)
			candidatePath := filepath.Join(filepath.Dir(currentPath), "candidate.json")
			writeRegistryFixture(t, candidatePath, test.cities)
			if _, err := addReplicaRegistryCity(journalPath, currentPath, candidatePath, replicaRegistryAddConfirmation); err == nil {
				t.Fatal("unsafe registry change was accepted")
			}
			current, err := loadReplicaRegistry(currentPath)
			if err != nil {
				t.Fatal(err)
			}
			reopened, err := openReplicaJournal(journalPath, current, nil)
			if err != nil {
				t.Fatalf("rejected change altered the journal: %v", err)
			}
			reopened.Close()
		})
	}
	journalPath, currentPath := registryAddFixture(t)
	candidatePath := filepath.Join(filepath.Dir(currentPath), "candidate.json")
	writeRegistryFixture(t, candidatePath, []replicaRegistryCity{{CityID: cityA, Destination: "wss://one.example/"}, {CityID: cityB, Destination: "wss://two.example/"}})
	if _, err := addReplicaRegistryCity(journalPath, currentPath, candidatePath, "wrong"); err == nil {
		t.Fatal("registry add accepted the wrong confirmation")
	}
}
