package main

import (
	"encoding/json"
	"fiatjaf.com/nostr"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestRestorePastInitialRequiresExactPriorRelease(t *testing.T) {
	for _, history := range []string{"none", "exact", "wrong-revision", "wrong-event", "wrong-city", "rejected", "wrong-author"} {
		t.Run(history, func(t *testing.T) {
			admin, creator := nostr.Generate(), nostr.Generate()
			adminPK, creatorPK := nostr.GetPublicKey(admin), nostr.GetPublicKey(creator)
			relay, db, err := newRelay(filepath.Join(t.TempDir(), "restore.db"), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			defer relay.DisableExpirationManager()
			p := unitOrganizerPolicy(relay, enableOrganizers(relay, db, adminPK))
			start := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
			proposal := nostr.Event{Kind: 31923, CreatedAt: nostr.Now() - 3*86400, Content: "Test walk", Tags: nostr.Tags{
				{"d", cityA + ":" + start.Format("2006-01-02")}, {"title", "BitcoinWalk Test City"}, {"summary", "BitcoinWalk in Test City"}, {"image", "https://example.com/image.jpg"},
				{"start", strconv.FormatInt(start.Unix(), 10)}, {"end", strconv.FormatInt(start.Add(time.Hour).Unix(), 10)}, {"D", strconv.FormatInt(start.Unix()/86400, 10)},
				{"location", "Square"}, {"location", "1,2"}, {"g", encodeGeohash(1, 2)}, {"t", "bitcoinwalk"}, {"r", "https://example.com/chat"},
				{"start_tzid", "UTC"}, {"end_tzid", "UTC"}, {"i", cityA}, {"bitcoinwalk", "initial-proposal-v1"},
			}}
			proposal.Sign(creator)
			// Seed retained historical state, not a new submission of a past event.
			if err := db.SaveEvent(proposal); err != nil {
				t.Fatal(err)
			}
			revision := draftEvent(t, creator, cityA, 1)
			var city map[string]any
			json.Unmarshal([]byte(revision.Content), &city)
			city["startAt"] = start.Format(time.RFC3339)
			body, _ := json.Marshal(city)
			revision.Content = string(body)
			revision.Tags = append(revision.Tags, nostr.Tag{"e", proposal.ID.Hex(), "", "initial-walk"})
			revision.Sign(creator)
			if err := db.SaveEvent(revision); err != nil {
				t.Fatal(err)
			}
			grant := cityGrant{CityID: cityA, CreatorPubkey: creatorPK.Hex(), CreatorRevisionID: revision.ID.Hex(), EditorPubkeys: []string{creatorPK.Hex()}, SuperAdminPubkey: adminPK.Hex()}
			accept(t, relay, workflowEvent(t, admin, 30302, grant, nostr.Tags{{"d", cityA}}, 2))
			decision := cityDecision{CityID: cityA, RevisionID: revision.ID.Hex(), InitialEventID: proposal.ID.Hex(), Status: "approved"}
			tags := nostr.Tags{{"d", cityA}, {"e", revision.ID.Hex(), "", "city-revision"}, {"e", proposal.ID.Hex(), "", "initial-walk"}, {"status", "approved"}}
			if history != "none" {
				prior := decision
				author := admin
				switch history {
				case "wrong-revision":
					prior.RevisionID = proposal.ID.Hex()
				case "wrong-event":
					prior.InitialEventID = revision.ID.Hex()
				case "wrong-city":
					prior.CityID = cityB
				case "rejected":
					prior.Status = "rejected"
				case "wrong-author":
					author = creator
				}
				old := workflowEvent(t, author, 30304, prior, tags, 3)
				if err := db.SaveEvent(old); err != nil {
					t.Fatal(err)
				}
			}
			accept(t, relay, workflowEvent(t, admin, 30304, cityDecision{CityID: cityA, RevisionID: revision.ID.Hex(), Status: "revoked"}, nostr.Tags{{"d", cityA}, {"e", revision.ID.Hex(), "", "city-revision"}, {"status", "revoked"}}, 4))
			if p.checkCalendarRead(proposal) == nil {
				t.Fatal("revoked walk readable")
			}
			restore := workflowEvent(t, admin, 30304, decision, tags, 5)
			if history == "exact" {
				accept(t, relay, restore)
				if err := p.checkCalendarRead(proposal); err != nil {
					t.Fatal(err)
				}
			} else {
				deny(t, relay, restore)
			}
			if err := p.validateInitialProposal(proposal, nil, true); err == nil {
				t.Fatal("new past submission allowed")
			}
		})
	}
}
