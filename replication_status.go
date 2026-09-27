package main

import (
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

var replicaPublicStatuses = []string{
	"acknowledged",
	"blocked",
	"canceled",
	"cancellation-required",
	"pending",
	"retry",
	"revocation-required",
	"revoked",
	"stale-source",
}

type replicaStatusCity struct {
	CityID      string         `json:"cityId"`
	Destination string         `json:"destination"`
	State       string         `json:"state"`
	Counts      map[string]int `json:"counts"`
}

type replicaStatusReport struct {
	Version    int                 `json:"version"`
	State      string              `json:"state"`
	Reconciled bool                `json:"reconciled"`
	Cities     []replicaStatusCity `json:"cities"`
}

func readReplicaStatusToken(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	credentialDirectory := os.Getenv("CREDENTIALS_DIRECTORY")
	systemdManaged := credentialDirectory != "" && filepath.Clean(filepath.Dir(path)) == filepath.Clean(credentialDirectory)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || (!systemdManaged && info.Mode().Perm()&0077 != 0) {
		return nil, errors.New("replica status token must be an owner-only regular file")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	token := strings.TrimSuffix(string(raw), "\n")
	decoded, err := hex.DecodeString(token)
	if err != nil || len(token) != 64 || len(decoded) != 32 || strings.ToLower(token) != token {
		return nil, errors.New("replica status token must contain exactly 64 lowercase hex characters")
	}
	return []byte(token), nil
}

func replicaCityStatus(counts map[string]int) string {
	for _, status := range []string{"retry", "stale-source", "cancellation-required", "revocation-required", "unknown"} {
		if counts[status] > 0 {
			return "degraded"
		}
	}
	if counts["pending"] > 0 || counts["blocked"] > 0 {
		return "pending"
	}
	return "healthy"
}

func buildReplicaStatus(journal *replicaJournal) (replicaStatusReport, error) {
	if journal == nil {
		return replicaStatusReport{}, errors.New("replica status journal unavailable")
	}
	summaries, err := journal.outboxSummary()
	if err != nil {
		return replicaStatusReport{}, err
	}
	report := replicaStatusReport{Version: 1, State: "healthy", Reconciled: journal.isReconciled(), Cities: make([]replicaStatusCity, 0, len(summaries))}
	if !report.Reconciled || journal.health() != nil {
		report.State = "degraded"
	}
	for _, summary := range summaries {
		counts := map[string]int{}
		for status, count := range summary.Statuses {
			if slices.Contains(replicaPublicStatuses, status) {
				counts[status] += count
			} else {
				counts["unknown"] += count
			}
		}
		state := replicaCityStatus(counts)
		if state == "degraded" {
			report.State = "degraded"
		} else if state == "pending" && report.State == "healthy" {
			report.State = "pending"
		}
		report.Cities = append(report.Cities, replicaStatusCity{CityID: summary.CityID, Destination: summary.Destination, State: state, Counts: counts})
	}
	return report, nil
}

func replicaStatusHandler(journal *replicaJournal, token []byte) http.HandlerFunc {
	expected := append([]byte("Bearer "), token...)
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		provided := []byte(r.Header.Get("Authorization"))
		if len(provided) != len(expected) || subtle.ConstantTimeCompare(provided, expected) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="bitcoinwalk-replication-status"`)
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		report, err := buildReplicaStatus(journal)
		if err != nil {
			http.Error(w, `{"error":"status unavailable"}`, http.StatusServiceUnavailable)
			return
		}
		encoder := json.NewEncoder(w)
		encoder.SetEscapeHTML(true)
		if err := encoder.Encode(report); err != nil {
			return
		}
	}
}
