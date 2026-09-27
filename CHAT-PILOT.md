# Members-only chat pilot — local core, not deployed

Implemented in `internal/chatstate`, with a separate Khatru adapter in `chat.go`.
The adapter can now run via `RELAY_CHAT_PILOT=true` as a disposable loopback-only
preview on port 3342, separate from the organizer executable mode. It does not
advertise complete NIP-29 support. Existing organizer mode is unchanged.

## Implemented and tested

- Administrator-signed group creation and scoped moderator grants.
- Signed self-service joins and voluntary leaving. Rejoining does not restore
  moderator privileges. No BitcoinWalk editor or approval permissions are granted.
- Member-only kind 9 message storage, historical delivery and live-delivery gates.
- Moderator removal, explicit ban and unban; bans persist across reopen and
  block same-key rejoining. Ordinary removal and voluntary leave are not bans.
- Separate group authorization: a moderator or member in one group has no
  privileges in another.
- Signature, event ID, transport-authenticated identity, timestamp, tag and
  size validation. Unsupported kinds/tags fail closed, not silently ignored.
- Atomic bbolt transactions for event deduplication, membership/ban state,
  message storage and a sequenced audit log. Administrator identity is bound
  to the database and cannot silently change on restart.
- History/live delivery rechecks current membership. A read gate is held during
  bounded delivery; removal waits for in-flight delivery and prevents any new
  delivery once committed. Bytes already delivered cannot be recalled.
- Real WebSocket tests exercise NIP-42 authentication, nonmember/anonymous
  denial, history/live access, city isolation, existing subscriptions after
  banning, denied rejoining/writes, broad-filter rejection and disabled COUNT.
- The Khatru adapter deliberately bypasses its normal broadcast path: the
  usual pre-broadcast hook does not hold authorization through the actual
  socket write. Delivery instead uses the store gate through bounded writes.
- Subscriptions bind to an authenticated identity. Changing identity discards
  registered chat listeners; a client must subscribe again. No unsynchronized
  reads of Khatru's authenticated-public-key slice are used by the adapter.
- Reads require one explicit group and kind 9. COUNT, negentropy, deletion,
  replacement and management data paths are not enabled. Historical filters
  are applied before counting the result limit. Socket writes have a two-second
  deadline; historical delivery has a five-second budget (plus a final write).

## Deliberately incomplete protocol boundary

`Apply` takes a NIP-42-verified identity supplied by trusted transport code. It
must never accept an identity asserted by a request body. The current package
does not itself perform NIP-42 authentication or expose any network listener.

The pilot interprets standard kinds 9007, 9000, 9001, 9021, 9022 and 9 as domain
commands. Explicit `bitcoinwalk-ban=true` on 9001 and
`bitcoinwalk-unban=true` on 9000 are **local extensions**, not standard NIP-29
ban semantics or known Armada features. Unban clears the ban without joining
the user. These commands need a compatible moderator UI and canonical exported
moderation representation before deployment.

Signed stores now persist relay-signed moderation effects and metadata
39000–39003 in the SAME transaction as membership and the original signed audit
record. Join/leave generate 9000/9001; canonical effects reference their source
request and use acceptance time. Unban remains a local extension excluded from
the client's membership-status view. Existing organizer grant migration and
full rebuild-from-ledger validation remain separate unfinished work.

`OpenSigned` requires a dedicated relay key, binds its public identity to the
database, refuses a changed key or an unsigned reopen, and refuses to silently
convert populated unsigned pilot databases. The secret is not stored in the DB.
The adapter exposes this public key in NIP-11 `self`. Metadata discovery is
public; kind 39002 member rosters remain member-only. A user's authenticated
9000/9001 query can retrieve only that user's status, even after removal.

Unchanged metadata retains its event ID. Changed same-second metadata uses a
bounded hash tie-break to satisfy NIP-01 replacement ordering without inventing
future timestamps. Exceeding that budget rejects the entire write transaction
with a retry-next-second error. A backwards clock similarly fails closed when
a changed projection would regress. Tests verify rollback and restart identity.

## Remaining integration gates

1. Provision and back up the actual relay service key securely. Signed atomic
   projections are implemented locally; add full crash/replay reconstruction
   validation and migration tooling before claiming complete NIP-29 support.
2. Extend the tested Khatru adapter for metadata and the final supported query
   profile. The current narrow adapter rejects broad/multi-group filters; it
   never falls back to the public organizer event store. Test authenticated
   identity switching, slow peers and disconnect/revocation stress before deployment.
3. Bound network write time and queue sizes. Delivery callbacks must not retain
   snapshots for later delivery or call back into Store. Slow peers must not
   hold up moderation indefinitely. Recheck membership at final delivery, not
   just subscription creation.
4. Handle NIP-29 previous references and Armada's actual message/tag shapes;
   the current narrow pilot rejects unsupported tags. Test a pinned Armada
   release rather than assuming kind 9 alone makes a compatible chat client.
5. Add rate limits, safe moderation audit access and backup/restore tests.
   Membership bans cannot prevent new Nostr identities.
6. Generate the stable city-page entry URL from verified provisioning state;
   implement sign-in -> explicit join -> correct Armada group. Open admission
   does not require a secret invitation token, unless the tested client does.
7. Prepare a separate staging installation and human test. Do not switch
   legacy chat DNS or migrate existing private history as part of this pilot.

## Local preview (2026-09-18)

### Chat access chooser

Chooser preview: http://localhost:3344/join-chat and
http://localhost:3344/city-test/join-chat . This separate listener preserves
the currently running disposable relay and its test messages. Future pilot
starts serve the same chooser directly on the port-3342 entry routes.

The responsive chooser includes a clear destination, browser access, an
official Get Armada link, an optional local browser preference (no automatic
redirect), and signer/privacy guidance. Native app opening is explicitly
disabled: localhost is not reachable from a phone, and public Android app-link
and iOS fallback tests are still pending. No fake installation detection or
custom URL-scheme assumptions. Full app/browser preference selection follows
when native app opening is verified. Production city pages are not changed.

Chooser handler tests and JavaScript syntax checks passed. Browser inspection
verified content and the browser button's correct Armada group navigation.
Security headers disallow third-party scripts, frames and forms; query-string
redirect targets are ignored. No signer or private-key input on this page.

- Armada source checkout: `../armada-pilot`, pinned commit
  `5b99f88d309052abc1eeb4f0b2ef437de086e709`. Dependencies installed using lockfile
  with lifecycle scripts disabled. Production build passed; upstream source
  remains unmodified. No upstream commit or push made.
- Armada served on http://localhost:3343; relay on ws://localhost:3342.
- Global join entry: http://localhost:3342/join-chat .
- Paid-city route demonstration: http://localhost:3342/city-test/join-chat .
  Both are local test groups on ONE disposable relay, not actual dedicated-city
  infrastructure or production city-page integration.
- Browser verified the global entry redirects to Armada's correct group,
  shows BitcoinWalk global-pilot as a members-only channel, and Join opens login.
  User extension sign-in/join/message test is still pending; no user key imported.
- Pilot generates its own temporary administrator and relay keys in memory;
  it does not store user private keys. Restart creates fresh groups/identities.
  Database artifacts remain in an OS temporary directory for diagnostics but
  are not a supported recoverable deployment. Do not use real/private content.
- Build-time app/search relays point at the local test relay; default Blossom
  and Concord AV lists are disabled. Analytics is off by upstream default.
  This does not certify that all upstream features avoid external services;
  test only plain text with a browser extension, not uploads, GIFs or calls.
- `client` tags now accepted with duplicate/shape validation. Read-only timeline
  queries may include polls/reactions/deletions alongside kind 9, but those
  kinds still cannot be published. Replies, rich media, profile/list persistence
  and Armada's custom moderation UI are not enabled or claimed compatible.

No VPS deployment, production DNS, user keys or legacy Zooid data were changed.

## Armada source compatibility findings

Reviewed official main-branch source on 2026-09-18 (not a pinned release or a
runtime compatibility test): `src/hooks/useGroupMembership.ts`,
`src/lib/platform.ts`, `src/pages/GroupPage.tsx`, `src/AppRouter.tsx` at
https://github.com/soapbox-pub/armada . Native NIP-29 group entry is
`/s/:server/:groupId`, where the server segment is URI-encoded relay location
(secure relays omit wss://; local ws relays keep the ws: marker). Open joining
publishes 9021 with h and optional code; no code is needed for our open groups.
The hook first tries an optional Zooid relay-level handshake which our pilot
rejects, then proceeds with the actual group join. Membership is read using
9000/9001 filtered by h and the signed-in user's p; that query is now supported.
Our stable branded /join-chat route should resolve to this group entry, not
Armada's Concord /invite route. No route has been deployed yet.

Verification: `go test -race ./...` and `go vet ./...` passed; the new socket
tests passed ten repetitions with race detection (`go test -race -count=10 .
-run '^TestChat'`). Repeating the entire suite three times in one process
hit the existing organizer tests' shared rate limiter, so that repeated full-suite
run did not pass. No production rate limiting was weakened to accommodate tests.
