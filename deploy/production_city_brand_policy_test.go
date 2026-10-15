package deploy_test

import (
	"os"
	"strings"
	"testing"
)

func TestProductionCityBrandPolicyInstaller(t *testing.T) {
	script, err := os.ReadFile("install-production-city-brand-policy-0.9.4.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	for _, required := range []string{
		"PRODUCTION-CITY-BRAND-POLICY-0.9.4-SHA256SUMS",
		"bitcoinwalk-relay-production-city-brand-0.9.4",
		"production-events.before.digest",
		"production-events.after.digest",
		"production-journal.before.digest",
		"production-journal.after.digest",
		"london.after-restart.json",
		"app-production-0.3.353",
		"prior production binary and version were restored",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("installer is missing %q", required)
		}
	}
	for _, forbidden := range []string{"production-events.before.db\" \"$database", "production-journal.before.db\" \"$journal"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("policy-only rollback must not replace live state: %q", forbidden)
		}
	}
}
