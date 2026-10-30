package deploy_test

import (
	"os"
	"strings"
	"testing"
)

func TestProductionOwnerEditorRequestsInstaller(t *testing.T) {
	script, err := os.ReadFile("install-production-owner-editor-requests-0.9.15.sh")
	if err != nil {
		t.Fatal(err)
	}
	text := string(script)
	for _, required := range []string{
		"bitcoinwalk-production-sponsor-catalogue-0.9.13",
		"bitcoinwalk-production-owner-editor-requests-0.9.15",
		"reconciliation.before.json",
		"reconciliation.after.json",
		`cmp "$backup/reconciliation.before.json" "$backup/reconciliation.after.json"`,
		`cmp -s "$backup/journal.before.digest" "$backup/journal.after.digest"`,
		"registry.before.json",
		"entitlements.before.json",
		"app-production-0.3.597",
		"staging-relay.before.json",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("installer is missing %q", required)
		}
	}
	if strings.Contains(text, `cmp "$backup/journal.before.digest" "$backup/journal.after.digest"`) {
		t.Fatal("normal replication progress must not fail a policy-only upgrade")
	}
}
