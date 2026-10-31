package main

import (
	"os"
	"strings"
	"testing"
)

func TestBangkokCombinedAcceptsExactEmptyReceiverBaseline(t *testing.T) {
	for _, path := range []string{
		"deploy/rehearse-bangkok-combined-0.9.15.sh",
		"deploy/activate-bangkok-combined-0.9.15.sh",
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		if !strings.Contains(text, `-city "$city" -allow-empty`) {
			t.Fatalf("%s does not explicitly accept the observed empty receiver baseline", path)
		}
		if strings.Contains(text, "or not events or") {
			t.Fatalf("%s still rejects an exact empty occurrence set", path)
		}
		for _, required := range []string{
			`report["replica"]["occurrenceIds"] != events`,
			`not report.get("exactEventIds")`,
			`not report[side].get("allSignaturesValid")`,
			`report[side].get("privateWrapperCount") != 0`,
		} {
			if !strings.Contains(text, required) {
				t.Fatalf("%s lost exactness gate %q", path, required)
			}
		}
	}
}
