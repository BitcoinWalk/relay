package main

import (
	"context"
	"fiatjaf.com/nostr"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWalkDelegationWebSocketAcceptance(t *testing.T) {
	admin, creator, nominee := nostr.Generate(), nostr.Generate(), nostr.Generate()
	adminPK, nomineePK := nostr.GetPublicKey(admin), nostr.GetPublicKey(nominee)
	relay, db, err := newRelay(filepath.Join(t.TempDir(), "events.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	defer relay.DisableExpirationManager()
	policy := enableOrganizers(relay, db, adminPK)
	walk, grant := delegationFixture(t, relay, admin, creator)
	server := httptest.NewServer(relay)
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	client, err := nostr.RelayConnect(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nostr.RelayOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	invite := walkInvite(t, creator, walk, nomineePK, "invite", "", 3)
	if err := client.Publish(ctx, invite); err == nil {
		t.Fatal("unauthenticated invitation accepted")
	}
	if err := client.Auth(ctx, func(_ context.Context, e *nostr.Event) error { return e.Sign(creator) }); err != nil {
		t.Fatal(err)
	}
	if err := client.Publish(ctx, invite); err != nil {
		t.Fatal(err)
	}
	accepted := walkAccept(t, nominee, invite, walk, 4)
	if err := client.Publish(ctx, accepted); err == nil {
		t.Fatal("acceptance used inviter authentication")
	}
	// The client library authenticates once per connection. A nominee uses
	// their own session rather than changing the inviter's authenticated socket.
	nomineeClient, err := nostr.RelayConnect(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nostr.RelayOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer nomineeClient.Close()
	client = nomineeClient
	if err := client.Publish(ctx, accepted); err == nil {
		t.Fatal("unauthenticated acceptance accepted")
	}
	if err := client.Auth(ctx, func(_ context.Context, e *nostr.Event) error { return e.Sign(nominee) }); err != nil {
		t.Fatal(err)
	}
	if err := client.Publish(ctx, accepted); err != nil {
		t.Fatal(err)
	}
	if policy.canEdit(nomineePK, &grant) {
		t.Fatal("websocket acceptance granted city-wide access")
	}
}
