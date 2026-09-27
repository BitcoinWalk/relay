# Deployment status — 2026-09-19

## NIP-52 organizer policy 0.4.0 — prepared, not installed

Approval-bound kind31923 write validation and stale/revoked public-read/broadcast
filtering implemented and local race/vet checks pass. Staged web builder: 75 tests
and TypeScript pass. No calendar publication performed; no real web activation.
See `../bitcoinwalk-calendar-staging/CALENDAR-STAGING.md` and
`../calendar-publication/STATUS.md` for deployment and next web/UI steps.

## Organizer revision retention 0.3.0 — staging installed, local web activated

New immutable revision/decision addresses retain approved snapshots during edits
and rejections. Legacy records remain readable; address replacement is rejected
at the storage boundary. User installed the upgrade; protected backup:
`/var/backups/bitcoinwalk-approved-revisions.rr06ol`. Public NIP-11 independently
verified 0.3.0, then matching web source activated locally. Chat and production
services are unchanged. See `../bitcoinwalk-approved-revisions/RETAINED-REVISIONS.md`.
End-to-end human approval/edit/reject acceptance and organizer editing UI remain pending.

## Current status: external Armada migration verified; hosted app retired

- [x] Local-only bundled-query characterization added in `chat_batch_test.go`. Actual authenticated WebSocket frames with temporary keys/DB: chat-only gets EOSE and own live echo; chat+profile in either filter order gets CLOSED for the shared subscription; rejected profile under a separate subscription ID leaves chat live. All four cases pass in three race-enabled repetitions. No production source, permissions, VPS, or real membership changes. Confirms mechanism, not actual live Armada request composition; exact browser subscription capture remains needed before attributing user's symptom to batching.

- [x] Prepared Austin multi-channel subscription support: up to 32 explicit unique group IDs; query admission requires at least one joined group, history skips unjoined groups under revocation gate, total result cap 200; live delivery checks actual event group rather than first requested group. Existing allowed kinds retained, no unscoped chat reads permitted. Extended WebSocket test includes unjoined group first, private history/live exclusion, same-user reauth and ban; new history cap/leave test. Full race suite, vet and static build pass.
- [x] Uploaded/verified `/home/bitcoinwalk/bitcoinwalk-austin-live`, archive `40ef33b374ca77a654176d0a7b5af0f0364b2cc8d69d8e9eff64000fb66ef357`; installed Austin binary confirmed expected metadata build. Installer backs up binary/key/DB, restarts only Austin; global chat/Caddy unchanged.
- [ ] User installs `sudo sh deploy/upgrade-austin-live.sh`; then reconnect both clients and verify live cross-user delivery without refresh. This addresses a confirmed filter incompatibility, not a proven complete explanation of all current Armada behavior. Earlier unscoped/wider-kind subscriptions remain rejected by design.
- [ ] Anonymous profile display unresolved. Inspected Armada source routes kind-0 queries to configurable app relays plus servers; chat backend has no profile store. No external profile lookup proxy or new profile publication enabled. Direct diagnostic join succeeded per user; ordinary Armada join still needs acceptance.

- [x] Diagnosed Armada settings compatibility: form always includes banner and empty about; banner unsupported, empty about rejected by generic tag helper. Added kind-9002-only banner validation/persistence/projection (empty clears; otherwise HTTPS without credentials; never fetched), empty about support, duplicate rejection. Appended omitempty field preserves older recovery serialization. Full race tests/vet pass, including exact settings shape, restart/recovery, banner clearing, unauthorized/public/closed rejection and open join.
- [x] Uploaded/checksum-verified Austin-only update `/home/bitcoinwalk/bitcoinwalk-austin-metadata`; archive `a1431d9609068fc4252a707c1965175028456e5cd42bb5f54b287f5ce6d9e1c2`. Installer guards old binary hash, stops only Austin, backs up binary/key and verified offline DB snapshot, installs and health-checks. On failure stops Austin and retains data; no stale DB rollback. Global chat/Caddy unchanged.
- [ ] User runs `sudo sh deploy/upgrade-austin-metadata.sh`, then refreshes Armada and saves existing channel settings with a fresh signature. Channel rename still unverified. Do not downgrade after new metadata writes; older binary lacks banner replay support.

- [x] User installed Austin HTTPS; backup `/var/backups/bitcoinwalk-austin-https.fS1ukD/Caddyfile`. Independent trusted HTTPS returns Austin health 200 and matching distinct relay identity; public WSS kind-39000 discovery returns EOSE with no groups. `/join-chat` returns intentional 503. Existing global and organizer public health both return 200.
- [ ] Human super-admin creates Austin channel in official Armada at `https://armada.buzz/s/austin-staging.bitcoinwalk.org`; use members-only visibility and open joining. Actual group ID and signed creation/name acceptance must be verified before city link activation. No group creation or signing performed by agent.

- [x] Austin private backend installed by user; independently verified all four services active, loopback health and distinct public key `dbb67877549224ddb970658b9a9622d19fa5f45b377fb89e1787d19920cb3c2e`. Key backup `/var/backups/bitcoinwalk-austin-key.ypsq4i/relay-key`. Authoritative DNS on all three servers resolves new VPS, no AAAA record.
- [x] Uploaded/checksum-verified `/home/bitcoinwalk/bitcoinwalk-austin-https`, archive `7169db228819fe58026f083311e21428d73de64d5f458677d404770196ebcd59`. Combined Caddy candidate validates on VPS without activation. Installer guards current config hash and backs up/restores on install failure; adds only Austin host. No live Caddy change performed.
- [ ] User runs `sudo sh deploy/install-austin-https.sh`; then independently verify certificate/public WSS and existing hosts before human-signed group creation. Austin `/join-chat` remains 503 until group provisioned.

- [x] User approved Austin dedicated-relay pilot without payment. Uploaded and checksum-verified `/home/bitcoinwalk/bitcoinwalk-austin-pilot`, archive SHA-256 `3da2d4b82d91c66f39ba41496f750f4b6794fd8cc870d4355b024544aeefc026`. Reuses exact installed delivery binary; shell syntax and unit verification checked. Separate service/key/database on loopback 3337; no root installation performed.
- [ ] User runs `sudo sh deploy/install-austin-private.sh` and adds explicit `austin-staging` DNS A record to `213.232.235.138` (currently legacy `.240`). Public HTTPS, human-signed group creation and dedicated test-city routing remain gated; see AUSTIN-PILOT.md. Existing services and city links unchanged.

- [x] User successfully installed cleanup v2. Recoverable archive: `/var/backups/bitcoinwalk-armada-retired.jVOz79`. Relay binary, key, database, membership and legacy services unchanged by cleanup.
- [x] Independently verified public `/join-chat` contains one official Armada channel link; old test page redirects to `/join-chat`; old channel URL redirects to armada.buzz; retired `/signer` returns HTTP 410. Public WSS returned channel metadata and EOSE.
- [x] Read-only SSH verification: chat relay, organizer relay and Caddy active; signer unit not found/inactive, no port 3336 listener, live hosted Armada and signer directories absent. Persistent chat loopback health passes.
- [ ] Local development sources and historical relay installers remain. No permanent deletion performed. Production migration and hardening remain separate work.

## Deployment history (earlier pending statements superseded by current status above)

- [x] First retirement attempt restored previous Caddy config (hash independently matched) after failed redirect check; Caddy/chat/signer all active and assets still present. Backup /var/backups/bitcoinwalk-armada-retired.pOtn7B. Root cause: Caddy parsed `redir /join-chat 302` as a path matcher plus destination; corrected to `redir * /join-chat 302`.
- [x] Uploaded/verified retry bundle /home/bitcoinwalk/bitcoinwalk-armada-cleanup-v2 (SHA-256 17bff979f0c34f1c078d5f2ce16f9649174c0e7b2724277fd229ba0effb2d406). Explicit redirect parsed correctly; script now validates/reuses retained identical join page and reports failed step/status. Retirement still pending user sudo retry.
- [x] User confirmed cross-client delivery works after AUTH fix and approved hosted-Armada retirement. Read-only preflight confirms current Caddy hash bc4e1ad944ee99117908868d5380cd35a1f92492a81e0197a08ae51d834401cb, active chat/signer, and exact archive targets.
- [x] Prepared/uploaded/checksum-verified /home/bitcoinwalk/bitcoinwalk-armada-cleanup, archive ee51bff0a587e3d12937f53c79077b822232464cc7b1572ba23fe077b47e83e5. Shell syntax and Caddy adaptation checked. Installer preserves organizer host, serves standalone /join-chat, redirects legacy web entry/channel paths, retires /signer with HTTP 410, disables signer unit and moves exact obsolete assets into protected recoverable archive. No relay binary, key or DB change.
- [ ] User runs sudo sh deploy/retire-hosted-armada.sh; independently verify public WSS/metadata, redirects, page and retired service afterwards. No live removal/retirement has occurred yet. Local development sources and historical relay installers remain; no permanent deletion approved/performed.
- [x] Investigated cross-user invisibility. Reproduced actual repeated AUTH dropping a member's active subscription (local regression failed with missing live event). Same-identity reauthentication now preserves listeners; actual identity changes still invalidate them. Cross-user history, live delivery, privacy and revocation tests pass with full race suite/vet. This establishes a relay defect, not yet the sole cause of the observed client issue.
- [x] Added content-free rate-limited read-rejection categories and filter shape diagnostics. Uploaded/verified /home/bitcoinwalk/bitcoinwalk-chat-delivery, archive f561a2df469fa2f196b8dd9e6aefa2900c403e6e1d839d8083ef5bb4dddefcc3.
- [ ] User installs sudo sh deploy/upgrade-chat-delivery.sh; then reconnect both external clients and test messages in both directions plus reload history. Hosted-app/signer retirement remains gated on successful cross-client checks.
- [x] User installed external-Armada preparation; protected backup /var/backups/bitcoinwalk-external-armada.X0TJQO. Public page independently confirmed to contain the single external channel link. External website/app login, joining and sending remain human acceptance gates before removing hosted Armada and signer.
- [x] User approved moving to external Armada only. One-button page prepared; source metadata queries now accept Armada's 500 requested limit while stored results remain capped at 200. Content-free/rate-limited rejection logging added to diagnose native send failure. Full race suite and vet passed.
- [x] External armada.buzz direct channel page independently rendered channel name and Join button without login. No signing or message publishing performed. Mobile sending remains unverified.
- [x] Prepared/uploaded/checksum-verified /home/bitcoinwalk/bitcoinwalk-external-armada (archive 39e2b90c281d3e01cc7c49eccca0c325e76d6cd7eb4f5c85d7fc22b3cf515508). User sudo installation pending. Hosted Armada and signer retained temporarily until external login/join/send pass; no code or server assets deleted yet. See EXTERNAL-ARMADA-MIGRATION.md for gated removal checklist.
- [x] User attempted Armada channel creation; public metadata confirms existing group b7082b86a614153e with default name BitcoinWalk b7082b86a614153e. Source inspection identifies unsupported follow-up kind 9002; do not create duplicate. Added admin-only name/about edits with fixed private/open membership policy, backwards-compatible omitted fields, recovery/restart/privacy tests. Full race suite and vet pass.
- [x] Naming upgrade uploaded/verified at /home/bitcoinwalk/bitcoinwalk-chat-naming, archive SHA-256 0cbfb48b50b9d103f50f6483dc131ed33d67980c7415f53883cd2ac09e388f04. Installer briefly stops chat, backs up binary/key and verified DB snapshot, then installs/starts new binary. Failure leaves service stopped and artifacts retained; no automatic stale-DB rollback.
- [ ] User installs naming upgrade and signs metadata update to chat for existing group. No channel has been renamed by the agent. Group provisioning is now partially complete, superseding earlier 'no groups' notes.
- [x] User installed signer update; Caddy backup /var/backups/bitcoinwalk-signer.ca4qvV/Caddyfile and old web build /srv/bitcoinwalk-chat-staging/armada-before-signer. Independently verified public /signer WSS and unauthenticated kind-24133 recipient subscription EOSE. No probe events published. Actual Amber pairing remains pending.
- [x] Implemented isolated self-hosted signing transport (kind 24133 only, no persistent storage/key) and explicit Armada signing-relay override. Go race suite/vet and public frontend build pass; new frontend tests pass. Full frontend suite has one untouched GifPicker test failure (1504 passed). Local commit attempt blocked by missing Git author name; code remains staged, no identity configuration changed or push attempted.
- [x] Uploaded and checksum-verified signer update at /home/bitcoinwalk/bitcoinwalk-signer-update; archive SHA-256 231dcba207dd8b6c1e0a187a74c0d445fce697d8609b5dda68198af80af90321. No live signer deployment performed. See SIGNER-STAGING.md.
- [ ] User runs sudo sh deploy/install-signer.sh; verify public /signer WSS, then fresh Amber pairing on GrapheneOS/Vanadium. Chat permissions unchanged; group provisioning still pending.
- [x] User installed public web staging; Caddy backup /var/backups/bitcoinwalk-chat-web.NvaizP/Caddyfile. Independently verified trusted HTTPS health, NIP-11 self 384d58d74e08d9c395828c3b96c132c09820dfda19d5460ef9453206b06373ba, Armada HTTP 200 and rendered welcome/login UI. Public WSS connected and metadata REQ completed with EOSE and no groups. /join-chat correctly returns 503. No group creation or user signing performed.
- [ ] Human admin signer/group provisioning remains next. In-app browser login did not expose a browser-extension option; use the user's extension-enabled browser, never request or import the admin nsec.
- [x] Prepared and uploaded public-URL Armada build (unmodified source 5b99f88d309052abc1eeb4f0b2ef437de086e709) and isolated HTTPS installer to /home/bitcoinwalk/bitcoinwalk-chat-web-staging. Archive SHA-256 838b4c1f352eaf426d6b2bd53a93ae835028bd111a68eec5cc5c7891e0759d30; extracted file hashes and shell syntax verified. Existing Caddy hash and DNS reconfirmed. No root install/reload performed by agent.
- [ ] User runs sudo sh deploy/install-chat-web.sh; then independently verify HTTPS/WSS and browser UI. /join-chat remains disabled until human admin group provisioning is verified. See deploy/CHAT-WEB-RELEASE.md for build limits and rollback behavior.
- [x] User successfully ran corrected credential update. Independently verified active/running/enabled, NRestarts=0 and loopback health response {"status":"ok","mode":"persistent-chat-staging"}. Organizer relay and Caddy remain active. Previous binary backup: /var/backups/bitcoinwalk-chat-binary.5vsqKr.
- [ ] Public HTTPS/Armada deployment, group provisioning and end-to-end membership checks remain pending. Backend health is not yet proof of public chat readiness.

## Resolved incident: systemd credential compatibility (chronological history)

- [x] First fix attempt rolled back before startup: unconditional reset-failed rejected an unloaded inactive unit. Read-only VPS check confirms unit exists, inactive/disabled, and original binary hash restored. Updated/uploaded script to reload and validate unit before binary replacement and reset only a failed unit; shell syntax checked. User retry pending; credential runtime compatibility still unverified.
- [x] User ran private chat installer; startup failed on credential validation. Installer stopped/disabled the new service; no public chat exposure occurred.
- [x] User's isolated LoadCredential diagnostic confirmed a regular file, mode 0440, size 65. Added narrow ACL validation accepting only root/current-owner plus read-only current-service-UID access; ordinary group-readable credentials remain rejected.
- [x] Credential tests (including actual Linux ACL), full race-enabled suite and go vet passed. Uploaded and checksum-verified fix bundle at /home/bitcoinwalk/bitcoinwalk-chat-credential-fix. Binary SHA-256: d9b80f0308129cbb62834e5c048e4da6d14b9c06fb16d85d59fa8a1454afd705.
- [ ] User runs sudo sh deploy/fix-chat-credential.sh there. Script backs up/replaces only the binary, starts the isolated service and checks loopback health; on failure it stops the service and restores the old binary. Key, database, service configuration and Caddy are not replaced. Live compatibility remains unverified until this succeeds.

## Earlier deployment history (superseded where noted above)

- [x] Built and uploaded private-backend bundle to /home/bitcoinwalk/bitcoinwalk-chat-staging-0.1.0 on the new VPS. Archive SHA-256: 9e5bfd376acf52272afa5a9c828c601e24409a2a3164a98f2cca41dbd382471b. Installer creates only a separate loopback service/key/config, never alters Caddy or legacy services. Full race-enabled test suite passed after retry fix.
- [ ] User runs sudo sh deploy/install-chat-private.sh from that directory. Service/key creation has NOT been performed by the agent. Public HTTPS/client deployment remains a separate gated step.

- [x] User added chat-staging DNS; independently verified all three authoritative Njalla servers and recursive lookup point to 213.232.235.138. Legacy chat, apex and www still point to 213.232.235.240. No public chat service activated yet.
- [x] Implemented bounded, cancellable retry for the specific atomic metadata-revision collision; ordinary policy/storage failures are not retried. Load testing remains pending.

## Persistent chat staging preparation (not deployed)

- [x] Added non-disposable staging startup, protected credential loading/exclusive key creation, and a separate hardened systemd unit template using LoadCredential.
- [x] Added consistent snapshot backup and restore test preserving messages, bans, metadata and relay identity; added audit replay verification and tamper detection on startup.
- [x] Added bounded per-IP/per-key limits and uniform subscription caps. Local tests/vet pass; load/slow-client/disk limits remain to verify.
- [x] User approved chat-staging.bitcoinwalk.org. Read-only VPS check: 36 GB free, Caddy and organizer active, loopback port 3335 available. All authoritative DNS servers currently resolve chat-staging to legacy 213.232.235.240; explicit A to 213.232.235.138 required.
- [x] Prepared isolated chat-staging Caddy site block and public environment file locally; neither uploaded nor activated. Existing Caddyfile remains unchanged.
- [ ] User adds explicit staging DNS record; verify propagation, finish exposure gates, validate proxy config and prepare backed-up deployment. No VPS or DNS mutations performed.
- [ ] Same-second metadata budget retry UX/load case: one randomized test run hit the limit; fresh full race-enabled run passed, but the edge case is not marked resolved. See STAGING-CHAT-PREP.md.

- [x] Debian 13 VPS at 213.232.235.138; SSH access as bitcoinwalk.
- [x] Pinned Khatru source and reproducible static build; modules checksum-verified.
- [x] Functional tests and go vet passed.
- [x] Uploaded build and source to /home/bitcoinwalk/bitcoinwalk-relay-setup.
- [x] User ran root installer. Independently verified service active and enabled, DynamicUser=yes, NRestarts=0.
- [x] Health endpoint returned {"status":"ok"}; relay binds 127.0.0.1:3334 only.
- [x] User-approved govulncheck and verbose review: 0 reachable vulnerable symbols, 1 imported-package finding, 32 other module-level findings.
- [ ] Dependency hardening: upgrade and retest fasthttp (GO-2026-4950, fixed v1.70.0), x/crypto (fixed findings through v0.56.0; deprecated OpenPGP has no fix and is not used), x/net (through v0.56.0), klauspost/compress (v1.18.7), x/sys (v0.44.0). No running binary changed by this scan.
- [x] User approved relay-staging.bitcoinwalk.org as the staging name.
- [x] DNS read-only check: apex, www, relay, radom, relay-staging and an arbitrary test name all resolve to 213.232.235.240. No AAAA/CNAME answers observed for these names. This suggests a wildcard, but only the Njalla zone view can confirm whether staging also has an explicit record.
- [x] User added staging DNS. Direct queries to all three authoritative Njalla nameservers confirm relay-staging -> 213.232.235.138 with no AAAA/CNAME. Recursive caches still returned the old address at verification time. Apex and www remain on 213.232.235.240.
- [x] User installed HTTPS proxy. Independently verified active Caddy, deployed staging-only config, loopback-only relay/admin ports, and externally trusted HTTPS returning 200 with {"status":"ok"} when targeting the new IP.
- [x] External WSS test to the new IP with staging SNI and full certificate verification: HTTP 101, validated WebSocket accept header, unauthenticated REQ for kind 31923 returned EOSE. No events published. This proves empty/public reads work, not organizer publishing or app integration.
- [ ] Review host/provider firewall, expose only HTTP/HTTPS for the proxy (keep SSH), then verify certificate and WSS end-to-end.
- [x] Uploaded deploy/install-https.sh and Caddyfile.staging; user executed setup and TLS is activated. Installer uses Debian's packaged Caddy and includes validation and backup/restore logic.
- [ ] Recheck ordinary DNS-based requests after cached legacy address expires. Previously confirmed old IP returns 404, new IP returns 200; no configuration fix needed for that cached-DNS symptom.
- [ ] Backup/restore, disk monitoring and stronger subscription/connection limits before production use.

No Njalla API credentials or authenticated registrar session were supplied; the user made the staging DNS change. The existing legacy site and Zooid relays are not migrated or modified. The management dashboard and app integration remain future work; organizer-mode progress is tracked below.

## Organizer iteration

- [x] User completed admin signer / NIP-42 / publish / unauthenticated read-back test on staging. Event ID a8b29109c685dd9cd06cfbf0002146fc5da901e4c7b5c1d0644dfac2bedc4e4a (requested ten-minute expiration).
- [x] Normal DNS-based staging health checked successfully after propagation.
- [x] Implemented opt-in organizer mode 0.2.0; detailed semantics and limitations in ORGANIZERS.md.
- [x] Local baseline and organizer tests passed, including WebSocket submissions, revocation, restart persistence and storage-boundary rechecks. go vet and Linux static build passed.
- [x] Prepared upgrade with offline database/binary backup and rollback on failure, plus local browser organizer test.
- [ ] User runs sudo upgrade on staging; verify active version after installation.
- [x] User reported successful human organizer -> admin grant -> organizer edit/read-back and self-approval-rejection test on staging.
- [x] Added browser steps 4–8 for a distinct collaborator: public-key selection, admin grant, assigned-city edit/read-back, denial on a second admin-only city, admin revocation, and denial of a fresh post-revocation edit. Grant-update helper tests and JS syntax checks passed; rendered page verified. No additional VPS binary upgrade required.
- [x] User reported successful second-editor grant, city isolation and post-revocation rejection through steps 4–8.
- [ ] App integration, revision history/approved snapshots, current-decision resolution, duplicate city-name review and production hardening. Production remains unchanged.

## NIP-29 iteration and Armada follow-up

- [x] User chose group/permission compatibility before web-app integration, with chat later.
- [x] Added staged design and explicit governance decision in NIP29-PLAN.md.
- [x] Implemented local verified legacy-grant -> group seed model and deterministic relay-signed metadata generation (39000–39003). Unit tests pass for revocation retention, role mapping, key separation, signature checking and malformed inputs; go vet passes.
- [x] Read-only audit returned two verified city authorizations, each with one editor and retained creator. Source IDs: a19f0fffc90809f86751ccba904dd2405868d6e1ca1386f05a0f9a60ae1432d5 and e6cf1c23c3be0726d9eab5b4f7182eff1ec1bbdc952f17aa4a17e191664aea3c. Audit did not publish or change permissions.
- [x] User approved voluntary creator resignation while preserving creator provenance and organizational global authority. Implementation is pending; live policy unchanged.
- [ ] Durable standard moderation engine, service-key handling, migration, independent-client test and app integration. No live NIP-29 support is advertised; staging remains unchanged.
- [x] Recorded Armada scope: paid-city branded invite links to dedicated city chat; free cities link to global chat only. Official Armada docs confirm external NIP-29 support, not tested BitcoinWalk runtime compatibility.
- [x] Added staged rebuild/migration plan for user-reported legacy Zooid chat.bitcoinwalk.org. Plan preserves legacy service until validated cutover; no production DNS or service changes made.
- [x] User confirmed members-only message visibility for both global and paid-city chats; recorded relay-enforced access and revocation acceptance criteria. This policy is not yet deployed and does not imply end-to-end encryption.
- [ ] Implement and test members-only access; validate Armada NIP-29 invite/deep-link flow, chat permissions and paid/free routing.
- [x] Added local internal/chatstate core with durable signed membership/moderation commands, member-only delivery gates, role isolation, persistent bans and concurrent-join tests. Full existing test suite and go vet passed. No transport adapter or live deployment yet; see CHAT-PILOT.md.
- [x] Follow-up: implemented a separate Khatru adapter in chat.go, instantiated by tests only. Real WebSocket tests cover NIP-42, private history/live delivery, revocation on existing subscriptions, isolation and disabled COUNT. No executable startup wiring or VPS deployment; metadata/standard moderation projections and Armada integration remain unfinished.
- [x] Added city-page invitation generation design: provisioned group mapping, stable branded entry URL, verified NIP-29 invite registration, rotation and membership checks. Implementation pending.
- [x] User confirmed self-service chat joining without moderator approval and moderator removal of spammers. Recorded open admission with members-only messages, explicit group-scoped ban/unban to prevent same-key rejoining, and abuse-control acceptance tests. Not yet implemented or deployed.
- [ ] Inventory, backup, parallel rebuild, migration rehearsal and approved global-chat cutover. See NIP29-PLAN.md stages 6–7.

### Signed chat discovery increment (local only)

- [x] Dedicated-key signed store, persistent relay identity binding, atomic relay-signed metadata and moderation effects. No actual VPS key generated.
- [x] NIP-11 self, public group discovery, member-only rosters and authenticated own-membership status queries in test adapter.
- [x] Tests cover signed metadata, same-second ordering, projection rollback, key mismatch/reopen, anonymous roster protection and Armada-shaped membership query over WebSockets.
- [x] Read Armada's official group routing and join/membership hooks; confirmed /s/:server/:groupId target and 9021 join flow without a code for open groups.
- [ ] Pinned Armada runtime test, remaining message/tag support, canonical replay validation, secure service startup/deployment and branded join route. No live NIP-29 claim or preview URL yet.

### Local Armada preview

- [x] Added responsive access chooser at localhost:3344/join-chat (and city-test equivalent) without restarting the active test relay. Browser access, optional browser preference and Get Armada link implemented; native app opening is disabled pending public-relay/device validation. Tests and browser navigation check passed.
- [ ] Enable tested Android app link and validate mobile browser/iOS fallback on real devices. No production chooser or native-app claim yet.

- [x] Pinned and built official Armada commit 5b99f88d309052abc1eeb4f0b2ef437de086e709; upstream source unmodified.
- [x] Added opt-in disposable loopback chat pilot (3342), served Armada build (3343), and fixed-target global/city-test join redirects. These are local demonstrations, not production city provisioning.
- [x] Browser verified private group discovery and Join -> login screen. Added Armada client-tag and combined timeline-filter compatibility; plain text only.
- [ ] Human extension sign-in, join, send and refresh verification. No user identity signed in by the agent. See CHAT-PILOT.md for URLs and limitations.
