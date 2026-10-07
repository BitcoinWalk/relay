# BitcoinWalk relay

BitcoinWalk's Khatru relay, organizer policy, community chat and paid-city
public-event replication implementation. Production activation remains a
separate, explicitly approved operation.

This repository is the canonical source. Generated binaries, staging app
archives, databases and credentials are deliberately excluded from Git. Exact
operator packages are built from reviewed commits and distributed separately
with SHA-256 manifests.

## Implemented

- Khatru from `fiatjaf.com/nostr`, pinned to `v0.0.0-20260916040958-27e395a0f6e7`.
- Durable bbolt event storage with the upstream eventstore adapter.
- Unauthenticated public Nostr reads (subject to query limits).
- NIP-42 write authentication matching the event author, plus an explicit writer allowlist. Initially only the BitcoinWalk super-admin is allowed.
- The base allowlist accepts kinds 0, 5, 30301, 30302, 30303, 30304 and 31923. Organizer mode replaces that broad writer check with the application policy, including its additional strictly validated workflow kinds.
- Upstream rate policies, 64 KiB WebSocket message limit, bounded queries, health check, NIP-11 metadata.
- Loopback-only binding, non-root systemd service with filesystem protection and 512 MiB memory limit.
- Tests for writer restrictions, NIP-42 authentication, invalid signatures, public WebSocket reads, persistence after reopening storage, health endpoint and NIP-11.

### Official city identity bindings

Organizer mode admits BitcoinWalk-specific kind `30312` records only through the
city identity policy. The event must be signed by the configured super-admin and
use the exact canonical JSON and ordered tags defined by the web contract. The
relay retains every sequence address, requires an existing city and current city
approval for activation or replacement, rejects suspended cities, stale
predecessors, forks and same-key replacement, and keeps exact retries
idempotent. Revocation remains available after approval or eligibility is lost
so an already-published identity can always be withdrawn. Private organizer and
city-signer proof events are not admitted.

## Build and test

The module targets Go 1.25. Dependencies are locked in `go.mod` and `go.sum`.

```sh
go mod verify
./test-report.sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false -trimpath -ldflags='-s -w' -o bitcoinwalk-relay .
```

## Opt-in paid-city replication

BW-56 adds an opt-in source journal, outbox, authenticated WSS transport and
destination receiver. Nothing is enabled without explicit operator settings.
The source journal requires both:

- `RELAY_REPLICA_JOURNAL`: path to a separate durable bbolt journal.
- `RELAY_REPLICA_REGISTRY`: path to an operator-owned JSON registry with mode
  `0644` or stricter. Group/world-writable files are rejected.

Example registry:

```json
{
  "version": 1,
  "cities": [
    {
      "cityId": "66f137cb-2ac1-4eef-8358-7dd66b45922f",
      "destination": "wss://madeira.bitcoinwalk.org/"
    }
  ]
}
```

Only exact root `wss://` endpoints are accepted. Destinations cannot come from
Nostr events or browser requests, duplicate cities/endpoints are rejected, and
an existing journal entry is never redirected when registry configuration
changes. The journal records configured-city grants, decisions, ordinary public
occurrences and cancellations in durable acceptance order. It excludes city
drafts, initial proposals, chat and unconfigured cities.

Before the relay begins serving traffic, it scans the authoritative event store
and reconciles any record that could have been committed immediately before a
crash but missed by the separate journal. Recovery is bounded, deterministic,
idempotent and must complete before freshness checkpoints become available.
Canceled occurrences remain in history but are recovered as ineligible, with
their tombstones ordered afterward. Changing a registry destination is rejected
as an implicit migration rather than redirecting retained journal entries.

Eligible occurrences are queued in a bounded durable outbox in the same bbolt
transaction as their journal entry. Each row retains the exact signed event,
validated dependency bundle, source checkpoint and fixed destination. Delivery
refreshes source eligibility, requires an exact positive acknowledgement, uses
bounded exponential retry state and stores only sanitized failure codes. Source
cancellation or city revocation suppresses pending work and marks already
acknowledged work as requiring ordered removal at the receiver.

Removal delivery retains and forwards the original signed control record. An
organizer cancellation sends the exact kind-5 tombstone only when its occurrence
reached the destination. An admin city revocation is a standalone ordered control
whenever destination state exists, and an in-flight occurrence retains its
revocation after acknowledgement. Revocation always supersedes an older
cancellation. Successful control acknowledgement leaves terminal `canceled` or
`revoked` status without erasing signed history.

The transport uses a service-signed kind-6000 wrapper over the registry's exact
root `wss://` destination and additionally requires NIP-42 authentication by the
same service identity. Wrappers are capped at 400 KiB inside a 512 KiB WebSocket
ceiling and are neither stored nor broadcast. Each connection has a five-second
deadline. A positive Nostr acknowledgement is returned only after the embedded
exact event/checkpoint has been accepted by the receiver.

To activate source delivery, set `RELAY_REPLICA_DELIVERY_KEY_FILE` to a regular
owner-only (`0600`) file containing exactly one 64-character hex service secret.
This must be a dedicated replication identity, never the super-admin or Guide
key. The worker processes at most 20 due rows per pass every five seconds and
retains bounded retry state.

To activate one destination receiver, configure all three settings together:

- `RELAY_REPLICA_RECEIVER_CITY`: the exact paid-city UUID.
- `RELAY_REPLICA_RECEIVER_SERVICE_PUBKEY`: the dedicated service public key.
- `RELAY_REPLICA_RECEIVER_DESTINATION`: the normalized root `wss://` endpoint,
  exactly matching the source registry.

Both source and destination require organizer mode. The outbox exposes a
content-free summary of city, destination and state counts for future admin
Alerts and BitcoinWalk Guide notifications.

Run the isolated TLS/WSS rehearsal before preparing an operator deployment:

```sh
go test -run '^TestReplicaTransportTLSSeparateRelays$' -count=1 -v .
```

The rehearsal uses separate source/destination databases, a real TLS WebSocket
listener, production NIP-42 and outbox/receiver code, an untrusted-certificate
negative check, exact occurrence delivery and ordered cancellation. It creates
no VPS configuration and uses generated ephemeral identities only.

The reviewed staging package is `bitcoinwalk-relay-replica-rehearsal-0.8.14`
with `REPLICA-REHEARSAL-SHA256SUMS` and
`deploy/install-replica-rehearsal.sh`. It creates a dedicated receiver on
loopback port 3341, generates a non-overwriting owner-only delivery key on the
VPS, exposes only the public key to receiver configuration, adds a fixed
one-city registry to the shared staging source and appends one exact Caddy host.
The installer snapshots the current source binary/database, unit inputs and
Caddyfile, validates their audited hashes, refuses existing targets or port
collisions and restores the source binary/Caddy configuration if activation
fails.

Before running it, an operator must create an explicit DNS record pointing the
chosen rehearsal hostname to `213.232.235.138`, select and review one staging
city UUID, transfer the complete package, verify its manifest and run:

```sh
sudo env \
  REPLICA_CITY_ID='<reviewed-staging-city-uuid>' \
  REPLICA_DESTINATION='wss://<explicit-staging-host>.bitcoinwalk.org/' \
  deploy/install-replica-rehearsal.sh
```

Do not use the wildcard address: unconfigured `*.bitcoinwalk.org` currently
resolves to the legacy `.240` host. Installation intentionally requires
interactive operator sudo and stops if DNS does not explicitly resolve to the
staging VPS. The Guide, chat, app, signer and production services are outside
the installer scope.

The inactive destination receiver validates the authenticated service identity,
fixed city and normalized destination, exact signed occurrence, dependency graph
and approval/grant checkpoint. It transactionally stages the immutable envelope
with a monotonic city head in the destination Bolt database, installs validated
dependencies first and makes the occurrence visible last. An interrupted install
returns no acknowledgement, blocks later checkpoints and resumes from the exact
same envelope. Exact replays are idempotent; stale/conflicting checkpoints,
changed receiver scope, retained destination tombstones and newer destination
revocations fail closed.

The Guide identity is notification-only. A future Guide AI agent may explain
status, help organizers troubleshoot and escalate problems, but it must never
hold replication credentials, alter destinations, approve cities, bypass relay
policy, or receive private chat content through this subsystem.

The `0.8.18` staging operator mode exercises the public receiver's negative
policy without creating an accepted event. It reads one already acknowledged
source envelope from the journal in read-only mode, then proves that a distinct
authenticated service, a foreign-city envelope and a correctly signed event by
an author absent from the current city grant are rejected. The rehearsal stops
the source while opening its journal, uses generated ephemeral test identities,
compares the stopped receiver database byte-for-byte before and after, and runs
the exact public-event audit again. It requires the explicit
`staging-policy-v1` confirmation and is not configured in either service unit.

Do not configure these variables in a production service yet. Two-city public
staging, Alerts, source-outage recovery and isolated restore/compaction have
passed. Entitlement-driven provisioning and explicit production acceptance
remain required.

The `0.8.27` maintenance mode compacts only offline BoltDB copies into a new,
nonexistent destination and verifies a bucket-sequence-aware logical digest.
The accepted operator rehearsal restarts unchanged live services before testing
the compacted copies on isolated loopback ports without delivery credentials.
It never installs a compacted database into a live path.

## Paid-city entitlement boundary

An organizer's `requestedTier: "paid"` preference and an admin city approval
are deliberately insufficient to provision replication. Provisioning consumes
an owner-only ledger of signed kind `30305` entitlement records from a separate,
explicitly configured entitlement authority. That authority must not be the
relay super-admin.

The replaceable entitlement is scoped to the immutable city UUID and exact plan
`bitcoinwalk-paid-city-lifetime-v1`. Its signed content carries only status and
a SHA-256 commitment to externally retained payment evidence; raw invoices,
preimages and customer payment details do not enter the relay. A newer signed
revocation supersedes an older activation.

The provisioning planner requires the exact `entitlement-provision-v1`
confirmation and creates one new owner-only candidate registry file. It refuses
an existing city or destination and binds the registry entry to the exact
current entitlement event ID. It neither edits the live registry nor starts a
receiver. The existing additive migration remains a separate backup-first
operator step and cannot remove or replace an existing entitlement binding.

Run the planner only with reviewed absolute paths and a separately verified
destination:

```sh
RELAY_REPLICA_ENTITLEMENT_PLAN_CURRENT=/etc/bitcoinwalk-replication/registry.json \
RELAY_REPLICA_ENTITLEMENT_PLAN_LEDGER=/etc/bitcoinwalk-replication/entitlements.json \
RELAY_REPLICA_ENTITLEMENT_PLAN_CANDIDATE=/var/backups/bitcoinwalk-provisioning/registry.candidate.json \
RELAY_REPLICA_ENTITLEMENT_PLAN_AUTHORITY='<entitlement-authority-hex-pubkey>' \
RELAY_REPLICA_ENTITLEMENT_PLAN_CITY='<immutable-city-uuid>' \
RELAY_REPLICA_ENTITLEMENT_PLAN_DESTINATION='wss://<reviewed-city-relay>/' \
RELAY_REPLICA_ENTITLEMENT_PLAN_CONFIRM=entitlement-provision-v1 \
./bitcoinwalk-relay
```

Successful output contains only city, destination, entitlement event ID and
registry digests. It does not expose the evidence commitment or payment data.

Activation uses the separate `entitlement-apply-v1` operator mode. It re-reads
the ledger immediately before mutation and rejects a candidate if the planned
entitlement was revoked, superseded or replaced. The legacy registry-add mode
cannot activate entitlement-bound candidates. Because apply advances the
journal's registry fingerprint, it must run only inside a backup-first deploy
transaction while the source writer is stopped; the same transaction installs
the exact candidate registry or restores the journal backup on failure.

Future production source services must set all three values together:

- `RELAY_REPLICA_REQUIRE_ENTITLEMENTS=true`
- `RELAY_REPLICA_ENTITLEMENT_LEDGER=<owner-only-ledger>`
- `RELAY_REPLICA_ENTITLEMENT_AUTHORITY=<64-character-hex-pubkey>`

With that gate enabled, startup fails closed unless every configured city is
bound to its exact current active entitlement. The current Memphis and Nashville
staging rehearsals intentionally retain their legacy unentitled configuration
and are not evidence of a paid entitlement.

The staging-only issuer mode uses exact confirmation
`entitlement-staging-issue-v1`, a distinct owner-only authority key and a signed
`basis=staging-synthetic-v1` tag. It can issue only an explicit active or revoked
test record into a new successor ledger file and never overwrites one. Release
`0.8.32` uses this mode only with an isolated synthetic baseline registry and
journal. A synthetic
record is not payment evidence and must never be copied into the production
entitlement ledger. The production payment service remains separate and must
supply its own authority and evidence commitment.

## Portable city endpoint directory

BW-60 begins with an application-specific signed kind `30309` directory chain;
this is a BitcoinWalk protocol record, not a claim that NIP-30309 is a standard.
The immutable city UUID and a locally trusted root event anchor the chain. Each
event binds exact JSON to indexed tags and carries a monotonic sequence, the
previous event ID, current owner, endpoint operators, one offline recovery key,
and one primary plus up to seven mirror `wss://` public-event endpoints.

The owner may update authorities and endpoints. An operator may update endpoints
only. Owner rotation must be signed by the current owner and changes nothing
else. The predeclared recovery key may replace the owner, preserves endpoints
and the recovery key, and clears all operators. A rotated owner loses authority.
Conflicting authorized successors fail closed; malformed or unauthorized relay
noise cannot manufacture a fork.

The bootstrap audit consumes one owner-controlled anchor file and an exact
owner-signed snapshot bundled with the release. Zero to eight independently
captured relay snapshots may attest to that bundle, but public relay
availability is never required to validate or retain the last-known-good
directory state. Every supplied attestation must resolve to the bundle's exact
current event and state; duplicate paths, stale attestations, unsafe file modes,
unknown JSON fields, invalid signatures and non-root WSS URLs are rejected.
It reads only and does not change the replication registry or live routing:

```sh
RELAY_CITY_DIRECTORY_AUDIT_ANCHORS=/absolute/path/anchors.json \
RELAY_CITY_DIRECTORY_AUDIT_BUNDLE=/absolute/path/signed-bundle.json \
RELAY_CITY_DIRECTORY_AUDIT_MIRRORS=/absolute/path/optional-attestation.json \
RELAY_CITY_DIRECTORY_AUDIT_CITY='<immutable-city-uuid>' \
./bitcoinwalk-relay
```

The separate mirror-consensus mode still requires two to eight independent
snapshot paths when no signed bundle is configured. Damus, nos.lol and other
public relays are optional transports, not directory authorities or activation
dependencies.

`RELAY_CITY_DIRECTORY_TRANSPORT=staging|production` runs a dedicated
directory-only relay from an exact anchor and owner-signed bundle. It seeds the
last-known-good chain at startup, preserves every kind-30309 predecessor instead
of applying normal addressable-event replacement, permits public reads, and
requires NIP-42 authentication plus current-chain authority for every write.
Admission is serialized so simultaneous authorized successors cannot both be
stored. Use a dedicated database and service; never point this mode at the
organizer or replica stores. The first staging service listens only on
`127.0.0.1:3343`; public Caddy and DNS activation are separate gates.

Publication, anchor creation, real network mirror fetching and routing changes
remain separate confirmation-gated increments. Clients must retain an explicit
trust anchor; a matching UUID alone does not prove ownership or official
BitcoinWalk recognition.

## Install on Debian 13

The deployment bundle is intended for `/home/bitcoinwalk/bitcoinwalk-relay-setup` on `213.232.235.138`.

Review `deploy/install.sh` and the service file, then run on the VPS:

```sh
cd /home/bitcoinwalk/bitcoinwalk-relay-setup
sudo sh deploy/install.sh
curl --fail http://127.0.0.1:3334/healthz
```

The installer checks hashes, refuses to overwrite an existing installation, copies a static binary to `/opt/bitcoinwalk-relay`, and enables a system service. It installs no Go compiler, database server or Docker. It does not change DNS, the firewall, SSH or other services. No permanent passwordless sudo is required.

Persistent data is managed by systemd under `/var/lib/bitcoinwalk-relay` (DynamicUser uses a protected private backing directory). Config defaults to the organizational admin public key, with no secret/private keys on the server.

Logs and status:

```sh
sudo systemctl status bitcoinwalk-relay --no-pager
sudo journalctl -u bitcoinwalk-relay -n 100 --no-pager
```

Stop/disable without deleting events:

```sh
sudo systemctl disable --now bitcoinwalk-relay
```

## Private preview after service installation

On your local machine:

```sh
ssh -N -L 3334:127.0.0.1:3334 bitcoinwalk@213.232.235.138
```

Then visit `http://localhost:3334/healthz`. Nostr clients can use `ws://localhost:3334`. There is only a text landing page, not a management interface.

## Before public release

- Choose and confirm an unused staging subdomain; inspect Njalla records before any edits. Preserve bitcoinwalk.org, www and all existing city/relay records.
- Add TLS reverse proxy, explicit canonical relay URL for authentication, trusted proxy handling, firewall review, and connection/subscription limits. Rate policies must be reviewed for proxy headers and address spoofing.
- Dependency scan completed with user approval on 2026-09-18: no known reachable vulnerable symbols. One imported-package finding and 32 additional module-level findings were reported in code the scanner does not find reachable. Dependency upgrades remain a hardening backlog item; this is not a guarantee of security. See DEPLOYMENT-STATUS.md.
- Add and test backups and restoration; never copy a live bbolt file as a backup. Use a transactional snapshot or stop the service before copying. No backups are configured yet.
- Add disk-capacity monitoring and alerting. The memory limit does not limit disk growth.
- Model free-walk submissions, global admin decisions, city editor permissions, and revocations. Do not broadly allow all writers to publish approval events.
- Integrate with the web app and verify unsigned public discovery and NIP-52 event interoperability.
- Build management dashboard, multi-city isolation, provisioning, payment integration and deliberate test-data migration as later stages.

References: https://pkg.go.dev/fiatjaf.com/nostr/khatru and https://khatru.nostr.technology/core/eventstore . Upstream documentation sometimes describes an older API; this build is checked against the pinned source.
