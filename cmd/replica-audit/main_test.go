package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeBaseline(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "baseline.json")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func validBaseline(cityID, eventID string) string {
	return `{"cityId":"` + cityID + `","readOnly":true,"source":{"url":"ws://127.0.0.1:3334","occurrenceIds":["` + eventID + `"],"allSignaturesValid":true,"privateWrapperCount":0},"replica":{"url":"ws://127.0.0.1:3341","occurrenceIds":["` + eventID + `"],"allSignaturesValid":true,"privateWrapperCount":0},"exactEventIds":true,"allowEmpty":false}`
}

func TestLoadAuditBaselineAcceptsExactSourceSnapshot(t *testing.T) {
	cityID := "be8514a4-9df0-4159-a517-71f65761cbbe"
	eventID := strings.Repeat("a", 64)
	report, err := loadAuditBaseline(writeBaseline(t, validBaseline(cityID, eventID)), cityID, false)
	if err != nil {
		t.Fatal(err)
	}
	if report.CityID != cityID || len(report.Source.OccurrenceIDs) != 1 || report.Source.OccurrenceIDs[0] != eventID {
		t.Fatalf("unexpected baseline: %#v", report)
	}
}

func TestLoadAuditBaselineRejectsUnsafeOrUntrustedSnapshots(t *testing.T) {
	cityID := "be8514a4-9df0-4159-a517-71f65761cbbe"
	eventID := strings.Repeat("a", 64)
	valid := validBaseline(cityID, eventID)
	for name, body := range map[string]string{
		"wrong city":         strings.Replace(valid, cityID, "586c0d1f-e861-4c8f-858c-ce3e2bfaf384", 1),
		"not exact":          strings.Replace(valid, `"exactEventIds":true`, `"exactEventIds":false`, 1),
		"bad signature":      strings.Replace(valid, `"allSignaturesValid":true`, `"allSignaturesValid":false`, 1),
		"private wrapper":    strings.Replace(valid, `"privateWrapperCount":0`, `"privateWrapperCount":1`, 1),
		"malformed event id": strings.Replace(valid, eventID, "not-an-event", 1),
		"unknown field":      strings.TrimSuffix(valid, "}") + `,"payload":"forbidden"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := loadAuditBaseline(writeBaseline(t, body), cityID, false); err == nil {
				t.Fatal("unsafe baseline accepted")
			}
		})
	}
}

func TestLoadAuditBaselineRequiresExplicitEmptyAllowance(t *testing.T) {
	cityID := "586c0d1f-e861-4c8f-858c-ce3e2bfaf384"
	body := `{"cityId":"` + cityID + `","readOnly":true,"source":{"url":"ws://127.0.0.1:3334","occurrenceIds":[],"allSignaturesValid":true,"privateWrapperCount":0},"replica":{"url":"ws://127.0.0.1:3342","occurrenceIds":[],"allSignaturesValid":true,"privateWrapperCount":0},"exactEventIds":true,"allowEmpty":true}`
	path := writeBaseline(t, body)
	if _, err := loadAuditBaseline(path, cityID, false); err == nil {
		t.Fatal("empty baseline accepted without -allow-empty")
	}
	if _, err := loadAuditBaseline(path, cityID, true); err != nil {
		t.Fatalf("explicit empty baseline rejected: %v", err)
	}
}
