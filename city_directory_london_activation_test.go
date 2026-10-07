package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLondonDirectoryActivationArtifacts(t *testing.T) {
	anchors, err := filepath.Abs("deploy/city-directory-anchors-0.8.80.json")
	if err != nil {
		t.Fatal(err)
	}
	bundle, err := filepath.Abs("deploy/city-directory-memphis-london-bundle-0.8.80.json")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"be8514a4-9df0-4159-a517-71f65761cbbe": "d16d969d0bf77c1a71453005b551ab7e2693d293888c2119928df93a69b21a79",
		"ca2f9905-fb4d-4948-a12c-c792b28ec7c8": "d8909ef6d7f675a28f13a02ddf4e13d083f6dfd519becf97b2c6a4aa63421fb2",
	}
	for city, root := range cases {
		result, err := auditCityDirectoryBundle(anchors, bundle, nil, city)
		if err != nil || !result.BundleVerified || result.CurrentEventID != root || result.Sequence != 0 || result.ChainLength != 1 {
			t.Fatalf("%s activation bundle invalid: %#v %v", city, result, err)
		}
	}
	londonBundle, err := filepath.Abs("deploy/city-directory-london-root-0.8.80.json")
	if err != nil {
		t.Fatal(err)
	}
	london, err := auditCityDirectoryBundle(anchors, londonBundle, nil, "ca2f9905-fb4d-4948-a12c-c792b28ec7c8")
	if err != nil || london.OwnerPubkey != "6534806c21772d8b35daf5570ebc10bf1540360ad792c46e0f48ea3a9b2de65f" || len(london.OperatorPubkeys) != 1 || london.PublicRelays[0].URL != "wss://london.bitcoinwalk.org/" {
		t.Fatalf("London signed root mismatch: %#v %v", london, err)
	}
	for _, path := range []string{"deploy/activate-london-directory-primary-0.8.80.sh", "deploy/activate-london-directory-secondary-0.8.80.sh"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		for _, required := range []string{"trap rollback", "events.before.db", "city-directory-anchors-0.8.80.json", "city-directory-memphis-london-bundle-0.8.80.json", "before-restart", "after-restart", "d8909ef6d7f675a28f13a02ddf4e13d083f6dfd519becf97b2c6a4aa63421fb2"} {
			if !strings.Contains(text, required) {
				t.Fatalf("%s lacks %q", path, required)
			}
		}
		if strings.Contains(strings.ToLower(text), "nsec1") {
			t.Fatalf("%s contains a private-key encoding", path)
		}
	}
}
