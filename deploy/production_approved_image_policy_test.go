package deploy_test

import (
	"os"
	"strings"
	"testing"
)

func TestProductionApprovedImagePolicyInstaller(t *testing.T) {
	script, err := os.ReadFile("install-production-approved-image-policy-0.8.98.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	for _, required := range []string{
		"PRODUCTION-APPROVED-IMAGE-POLICY-0.8.98-SHA256SUMS",
		"bitcoinwalk-production-approved-image-0.8.98",
		"production-events.before.digest",
		"production-events.after.digest",
		"production-journal.before.digest",
		"production-journal.after.digest",
		"london.after-restart.json",
		"app-production-0.3.349",
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
