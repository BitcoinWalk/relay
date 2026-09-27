# BitcoinWalk NIP-29 migration and Armada roadmap

Status: local model and metadata generation implemented; relay activation is NOT implemented or advertised. Staging remains organizer mode 0.2.0. No live keys have been generated and no permissions changed by this work.

## Decisions

- One city UUID maps to one NIP-29 group ID. The relay URL and its signing public key are also part of group discovery; preserving UUID alone does not transparently migrate clients between free and paid relays.
- Free cities share a relay; paid-city relay provisioning remains separate future work. Never redirect bitcoinwalk.org/www or existing city relays as part of this change.
- Public reading, visible group metadata, restricted writing and closed membership for the initial organizer-permissions milestone. Chat and invitations follow in the Armada milestone below; audio/video remain out of scope. Chat messages are members-only, as confirmed by the user; this is separate from public walk data.
- Metadata kinds 39000–39003 describe the group, privileged roles, membership and available roles. Use a separate relay service key and publish it as NIP-11 self; retain the organizational contact public key as pubkey.
- No user nsec is requested or stored. Relay signing does not authorize the service to sign user proposals, organizational approvals or calendar events on their behalf.
- Keep application approvals separate: a group/editor grant does not approve a walk or turn a test proposal into a calendar event.
- NIP-52 kind 31923 remains the eventual calendar format, with a matching h tag for its city group. Calendar publication stays disabled until tied to an approved revision. Cross-app discoverability must be tested, not assumed.
- Do not dual-write competing authorities. Cut over each city from custom 30302 grants to a canonical NIP-29 moderation log; reject further 30302 changes for migrated cities. A compatibility view, if needed, is derived and read-only.

## Role mapping

| Existing authority | NIP-29 role | Capability |
| --- | --- | --- |
| BitcoinWalk super-admin | super-admin in each city | Manage editor roles and approve/revoke BitcoinWalk revisions; cross-city access remains explicit policy |
| City creator | owner | Submit city revisions; cannot approve walks or elevate other users |
| Whitelisted editor | organizer | Submit revisions for assigned city only |
| Future ordinary member | no privileged role | No walk editing, approvals or role management; chat deferred |

Organizers must not be given the generic client assumption that every admin-like role can grant privileges. Advertise role descriptions and enforce capabilities server-side. Another member or organizer cannot remove or demote the creator or super-admin.

Confirmed by the user on 2026-09-18: allow voluntary resignation via kind 9022 while keeping immutable creator provenance (never removal by another editor). The organizational super-admin retains its out-of-band global authority. Leaving must remove active editing authority; replay, restart, migration and compatibility views must not silently restore it from the historical creator field. Reinstatement requires an explicit authorized grant. This policy is approved but not yet implemented; the live organizer relay still uses the previous policy.

## Stages and acceptance checks

### 1. Mapping and audit

- [x] Signature-verify existing admin grants, city ID, creator and editor keys.
- [x] Resolve latest grants with NIP-01 replacement ordering; do not union revoked editors back in.
- [x] Deterministic metadata generation with a dedicated relay key; tests use disposable generated keys only.
- [x] Read-only audit command: go run ./cmd/nip29-audit. Refuse truncated/time-out results rather than treating them as empty or complete.
- [ ] Compare dry-run report with a stopped/consistent database snapshot before cutover; remote query completeness alone is not proof of backup completeness.

### 2. Durable NIP-29 group engine

- [ ] Implement permission-checked standard moderation (9000/9001/9002/9007), closed join rejection and agreed voluntary-leave semantics. Explicitly reject unsupported destructive/invite/chat operations.
- [ ] Require exact h group references on group writes, matching NIP-42 identity, valid signatures, bounded publication time and valid supplied previous references. Do not silently ignore contradictory duplicate tags.
- [ ] Keep an append-only canonical moderation sequence and recovery checkpoints. Commit accepted operations and derived state durably; recover pending metadata generation after crashes. Never mutate permissions solely from uncommitted in-memory state.
- [ ] Rebuild roles, membership and relay-signed metadata from the same canonical sequence. Test removal, replay, duplicate requests, stale events, concurrency and crash/restart behavior.
- [ ] Authorize application proposals from this same group state; preserve no-self-approval and cross-city isolation. Freeze old grant writes after per-city cutover.

### 3. Controlled staging migration

- [ ] Stop writes briefly and take database/binary/config backups.
- [ ] Generate a relay service key on the VPS with restrictive permissions and systemd credential delivery; never print it or put it in an environment variable. Back up the key with the state, separately from public source code.
- [ ] Seed standard create/metadata/member-role moderation history from verified latest grants with recorded migration provenance. Metadata projections alone are not sufficient NIP-29 implementation.
- [ ] Gate new mode explicitly; preserve rollback artifacts. After new moderation writes exist, rollback requires reconciliation, not blindly re-enabling stale 30302 permissions.
- [ ] Verify creator/admin/editor roster equality, retained revocations, public reads, no chat and no changed approval state before enabling writes.

### 4. Compatibility testing

- [ ] Verify relay-signed 39000–39003 events, NIP-11 self, h-tag filtering and naddr links.
- [ ] Re-run human creator/editor/admin and revoke/isolation tests using standard moderation events.
- [ ] Use an independent NIP-29 client to discover/read groups and exercise supported moderation. A parser unit test alone does not establish client compatibility.
- [ ] Advertise NIP-29 support only once the implemented profile and its limitations have passed validation.

### 5. Web-app integration

- [ ] Resolve NIP-29 roles in organizer/admin screens; preserve extension-first signing.
- [ ] Fix revision history/approved snapshots and latest approval/revocation handling before publishing pages from staging.
- [ ] Add approved calendar publication and validate NIP-52 discovery in intended external apps.
- [ ] Keep presentation components separate so later custom UX/UI can replace the test interfaces.

### 6. Armada chat integration (depends on stages 2–4)

Confirmed scope on 2026-09-18:

| Tier | Branded entry URL | Chat backend |
| --- | --- | --- |
| Paid city | https://<city>.bitcoinwalk.org/<armada-invite> | Dedicated city's Khatru NIP-29 relay |
| Free city | https://chat.bitcoinwalk.org/<armada-invite> | Shared global Khatru NIP-29 chat relay; no dedicated free-city chat |

`<armada-invite>` is a requirement placeholder, not a verified literal Armada route. Armada is the client, not the relay implementation. Its current router distinguishes Concord/Buzz invitations from NIP-29 server/group routes; do not route a NIP-29 invitation into a Concord invite handler merely because both contain an naddr.

- [x] Confirm Armada's documented external NIP-29 relay support and self-hostable web frontend.
- [x] Implement local durable chat-state core and tests: authenticated signed commands, open joining, scoped moderators, bans, atomic membership/message storage, guarded history/live delivery. See CHAT-PILOT.md for protocol limitations; this is not yet a connected or complete NIP-29 relay.
- [x] Connect the core to a separate Khatru test adapter: authenticated WebSocket writes, gated history/live writes, identity-bound subscriptions, broad-filter rejection and disabled COUNT/negentropy. Actual socket tests pass; adapter is not enabled by the executable or deployed.
- [ ] Pin and review an Armada release, its NIP-29 deep links, join/invite handling and required event kinds. Prove invite -> correct relay/group -> authentication -> admission -> message/read-back against Khatru before promising compatibility.
- [ ] Implement and test required NIP-29 chat/invitation operations, scoped to chat groups. Ordinary chat membership must never grant organizer, approval or role-management rights. Keep organizer permission groups separate from chat groups and their visibility policies.
- [x] User confirmed members-only message visibility for both global and paid-city chats. This is access control, not end-to-end encryption. Walk pages/calendar events remain public.
- [ ] Enforce chat reads and writes server-side against authenticated, current membership of the specific chat group. An invite URL or relay authentication alone is not membership. Cover historical queries, broad filters, live subscriptions and alternative query paths so nonmembers cannot retrieve messages by bypassing Armada's UI.
- [ ] Test anonymous and authenticated nonmember denial, correct-group member access, cross-group isolation, voluntary leave and revocation. Stop further message delivery and historical retrieval when membership ends, including on already-open subscriptions. Previously downloaded messages cannot be recalled. Admission rules and any pre-join history restriction remain separate decisions; do not infer them from members-only visibility.
- [ ] Add a Join chat link to city pages: paid cities resolve their dedicated chat; free cities resolve the global chat. Payment provisions a city service, not an assumed per-member subscription.
- [ ] Implement branded entry routes without replacing city landing pages, event routes or NIP-05/Lightning endpoints. Prefer one maintained Armada web build with validated city/group deep-link entry routes. Reserve a non-conflicting invite prefix; verify SPA routing, HTTPS, WSS and NIP-11 content negotiation together.
- [ ] Validate invite expiration/revocation and replay policy; avoid leaking bearer invite codes into analytics, referrers and proxy logs. Do not treat an invite URL as authorization without server-side admission checks.
- [ ] Review frontend license obligations, external profile/media services and browser security configuration. Preserve BitcoinWalk's extension-first signing and no server-side user nsec policy.
- [ ] Test paid/free routing, wrong-city access, revoked membership, expired invites, mobile browsers and normal walk-page navigation. Voice/video and full custom chat UI are separate backlog items.

#### City-page invitation generation (design; implementation pending)

- Generate the page's Join chat target from trusted provisioning state, not a manually pasted organizer URL. Store the city tier, chat relay URL, relay self public key, chat group ID, branded entry route, readiness and admission policy. Enable the button only after group provisioning and end-to-end checks succeed; make repeated provisioning idempotent.
- Proposed stable public routes: paid https://<city>.bitcoinwalk.org/join-chat and free https://chat.bitcoinwalk.org/join-chat. These are BitcoinWalk entry routes, not claimed native Armada routes. Keep the public page URL stable when underlying invitations rotate; do not put a permanent bearer secret into the city record.
- Resolve the group using NIP-29's naddr for kind 39000, relay self public key, group ID and relay hint. If code-based admission is selected, generate a cryptographically random code (at least 128 bits), register it through an authorized kind 9009 operation and verify acknowledgement before sharing. The standard group reference supports naddr...?invite=<code>; join requests use kind 9021 with h and code tags. Verify the pinned Armada client's actual handling or implement a tested entry adapter; do not assume its Concord /invite route handles NIP-29.
- The signed-in visitor requests membership; grant only ordinary chat membership after admission succeeds, then open the correct Armada group. Existing members go straight to their chat. Signed requests, NIP-42 authentication and current membership must agree; opening a link alone grants no message access.
- User confirmed self-service admission for both global and paid-city chats: anyone can sign in with their Nostr identity and explicitly join without moderator approval. Authenticate and durably admit the member before granting message access; public entry links do not make messages publicly readable. Use private/restricted chat groups with open joining, distinct from closed organizer-permission groups. A public entry link need not carry a bearer code for open admission; retain code-based invitations only if required by the tested Armada flow, without bypassing moderation policy.
- User confirmed moderators can remove spammers. Provide explicit remove-and-ban and unban actions scoped to the chat group. A durable banned-pubkey rule must deny subsequent join requests and invitation redemption, terminate further live delivery and deny historical reads/writes; ordinary voluntary leave remains rejoinable. Do not silently interpret every standard NIP-29 remove-user event as a permanent ban: define, persist and audit the separate moderation policy and expose capabilities honestly to clients. Chat moderation must not grant walk editing or approval authority.
- Test self-service admission, duplicate/concurrent joins, unauthorized moderation, removal with active subscriptions, banned-key rejoin through every entry route, unban, and restart persistence. Public-key bans cannot stop a person creating a new identity; include join/write rate limits and abuse monitoring, without claiming identity-level exclusion.
- Scope invitation creation/rotation to explicitly authorized chat managers (not automatically every walk editor). Design expiration, use limits and revocation as enforced relay policies; these are not fully specified merely by NIP-29 kind 9009. Persist them across restarts, protect concurrent redemption and prevent removed/banned identities from bypassing policy by reusing public invitations. Revoking an invitation is distinct from removing existing members.
- Acceptance checks: paid -> correct dedicated group, free -> global group, unprovisioned service disabled, no arbitrary redirect targets, authentication cancellation, pending/denied requests, existing members, rotated/expired/revoked codes, concurrent redemption, removed-member rejoin policy and city tier changes. Never silently migrate private membership/history when a city upgrades or downgrades.

### 7. Rebuild and migrate global chat (depends on stage 6 acceptance)

User reports chat.bitcoinwalk.org currently runs Zooid on the legacy VPS. No inspection or migration has been performed. Keep it running until a tested cutover; do not touch apex, www, wildcard records or unrelated city services.

- [ ] Read-only inventory of existing software/protocol, database, messages, groups, memberships, roles, invite formats, relay public identity, attachments and DNS/proxy configuration. Determine whether existing history and links can be preserved; report incompatibilities before migration.
- [ ] Take and restore-test backups, including any existing relay signing key using secure handling (never paste keys into chat). A new key changes relay-signed group addresses; do not assume preserving the hostname preserves invitations.
- [ ] Build a parallel Khatru global chat service and Armada frontend on the new Debian VPS under an approved staging hostname. Do not overwrite Zooid or change production DNS for this test.
- [ ] Import supported data with explicit mappings; verify message counts, memberships, moderator capabilities, retained revocations and attachment access. Do not blindly copy a Zooid database into Khatru.
- [ ] Test with actual Armada clients and user-signed test accounts. Confirm backup/restore, monitoring and resource limits.
- [ ] Agree a cutover window, brief write freeze/final data sync, validation and rollback strategy that accounts for new writes. Then switch only the explicit chat hostname after approval. Keep legacy backups until retention is agreed.

Sources checked 2026-09-18: https://github.com/soapbox-pub/armada ; https://raw.githubusercontent.com/soapbox-pub/armada/main/src/AppRouter.tsx ; https://soapbox.pub/blog/how-to-self-host-armada . The supplied gitworkshop URL could not be fetched by the browser tool; these are official project sources. Runtime compatibility remains unverified.

## Source of truth

Reviewed current NIP-29: https://github.com/nostr-protocol/nips/blob/master/29.md (draft, optional). Library pinned in go.mod: fiatjaf.com/nostr v0.0.0-20260916040958-27e395a0f6e7. The existing library provides event structures/builders, not BitcoinWalk's authorization or an atomic migration workflow.
