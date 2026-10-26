// archive-initials imports exact released originals into an OFFLINE journal
// dependency bucket. It opens source storage read-only and creates no outbox rows.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"slices"
	"time"

	"fiatjaf.com/nostr"
	"fiatjaf.com/nostr/eventstore/boltdb"
	"go.etcd.io/bbolt"
)

const admin = "90cf043861e5b5a9972cb7b529a5ba71b215d6d1e314c749d5526ec133f1db73"

var reviewed = map[string]string{
	"1c2e8ad1267f38c742f4cda0b2c45a72955647aed86f0ec76bc43ea3f4a1ab02": "e62592ac-a5fc-42df-8f31-94416f6ee8d5",
	"ca7948e7d4dbc0eb9b944c2f7390ace4283e334aebe045069d83f7e6c5413245": "83f15504-9fca-4d59-a0f6-616a559510df",
	"338a7f139ab49382654f9270bf7f610d2d2df5e2482fa9dbcfee50c4229d0dd7": "83f15504-9fca-4d59-a0f6-616a559510df",
}

func tag(e nostr.Event, key string) string {
	value := ""
	count := 0
	for _, t := range e.Tags {
		if len(t) > 1 && t[0] == key {
			value = t[1]
			count++
		}
	}
	if count != 1 {
		return ""
	}
	return value
}
func byID(db *boltdb.BoltBackend, id string) *nostr.Event {
	parsed, err := nostr.IDFromHex(id)
	if err != nil {
		return nil
	}
	for e := range db.QueryEvents(nostr.Filter{IDs: []nostr.ID{parsed}}, 1) {
		if e.ID.Hex() == id {
			return &e
		}
	}
	return nil
}
func validate(db *boltdb.BoltBackend, e nostr.Event, scope map[string]string, authority nostr.PubKey) error {
	city, ok := scope[e.ID.Hex()]
	if !ok || e.Kind != 31923 || !e.CheckID() || !e.VerifySignature() || tag(e, "i") != city || tag(e, "bitcoinwalk") != "initial-proposal-v1" || tag(e, "expiration") != "" {
		return errors.New("unreviewed or invalid original")
	}
	released := false
	seen := 0
	for approval := range db.QueryEvents(nostr.Filter{Kinds: []nostr.Kind{30304}, Authors: []nostr.PubKey{authority}}, 1001) {
		seen++
		if seen > 1000 {
			return errors.New("approval scan exceeded bound")
		}
		var d struct {
			CityID     string   `json:"cityId"`
			RevisionID string   `json:"cityRevisionId"`
			Status     string   `json:"status"`
			InitialID  string   `json:"initialEventId"`
			InitialIDs []string `json:"initialEventIds"`
		}
		if json.Unmarshal([]byte(approval.Content), &d) != nil || d.CityID != city || d.Status != "approved" || !approval.CheckID() || !approval.VerifySignature() {
			continue
		}
		ids := d.InitialIDs
		if len(ids) == 0 && d.InitialID != "" {
			ids = []string{d.InitialID}
		}
		if !slices.Contains(ids, e.ID.Hex()) {
			continue
		}
		revision := byID(db, d.RevisionID)
		if revision == nil || revision.Kind != 30303 || revision.PubKey != e.PubKey || !revision.CheckID() || !revision.VerifySignature() {
			continue
		}
		var c struct {
			CityID string `json:"cityId"`
		}
		if json.Unmarshal([]byte(revision.Content), &c) == nil && c.CityID == city {
			released = true
		}
	}
	if !released {
		return errors.New("original lacks exact local signed release and creator revision")
	}
	seen = 0
	for deletion := range db.QueryEvents(nostr.Filter{Kinds: []nostr.Kind{5}, Tags: nostr.TagMap{"e": {e.ID.Hex()}}}, 1001) {
		seen++
		if seen > 1000 {
			return errors.New("deletion scan exceeded bound")
		}
		if deletion.CheckID() && deletion.VerifySignature() && (deletion.PubKey == e.PubKey || deletion.PubKey == authority) {
			return errors.New("original was canceled; refusing recovery")
		}
	}
	return nil
}
func archive(source, journal, originals string) error {
	data, err := os.ReadFile(originals)
	if err != nil {
		return err
	}
	if len(data) > 512*1024 {
		return errors.New("input too large")
	}
	var events []nostr.Event
	if err = json.Unmarshal(data, &events); err != nil {
		return err
	}
	if len(events) != len(reviewed) {
		return errors.New("expected exactly three reviewed originals")
	}
	sourceDB, err := bbolt.Open(source, 0600, &bbolt.Options{ReadOnly: true, Timeout: time.Second})
	if err != nil {
		return err
	}
	defer sourceDB.Close()
	// BoltBackend.Init performs migrations, so do not invoke it on a snapshot
	// that must remain byte-for-byte read-only.
	db := &boltdb.BoltBackend{Path: source, ReadOnly: true, DB: sourceDB}
	seen := map[string]bool{}
	for _, e := range events {
		if seen[e.ID.Hex()] {
			return errors.New("duplicate original")
		}
		seen[e.ID.Hex()] = true
		if err = validate(db, e, reviewed, nostr.MustPubKeyFromHex(admin)); err != nil {
			return err
		}
	}
	j, err := bbolt.Open(journal, 0600, &bbolt.Options{Timeout: time.Second})
	if err != nil {
		return err
	}
	defer j.Close()
	return j.Update(func(tx *bbolt.Tx) error {
		if tx.Bucket([]byte("metadata")) == nil {
			return errors.New("journal is not initialized")
		}
		bucket, err := tx.CreateBucketIfNotExists([]byte("approved-initial-archive-v1"))
		if err != nil {
			return err
		}
		for _, e := range events {
			data, err := json.Marshal(e)
			if err != nil {
				return err
			}
			key := []byte(e.ID.Hex())
			if old := bucket.Get(key); old != nil && !bytes.Equal(old, data) {
				return errors.New("existing dependency differs")
			}
			if err = bucket.Put(key, data); err != nil {
				return err
			}
		}
		return nil
	})
}
func main() {
	source := flag.String("source", "", "read-only source snapshot")
	journal := flag.String("journal", "", "offline journal candidate")
	originals := flag.String("originals", "", "reviewed original events JSON")
	flag.Parse()
	if err := archive(*source, *journal, *originals); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(`{"archivedInitials":3,"sourceReadOnly":true,"notificationsSent":0}`)
}
