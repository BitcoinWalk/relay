# External Armada transition

User approved hosting BitcoinWalk relays only, with one external Armada button.
Final destination: https://armada.buzz/s/chat-staging.bitcoinwalk.org/b7082b86a614153e
App opening is OS-dependent; website fallback is intentional. No custom Armada
fork/build is needed in the target architecture.

## Stage 1: prepare and diagnose (current)

- [x] Replace two-button test page with one external link; remove hosted-login,
  signer-pairing and browser-preference guidance.
- [x] Accept Armada's 500-item discovery request while retaining the existing
  200-result storage cap. Regression tests cover public discovery without
  leaking private roster; full race suite and vet pass.
- [x] Add rate-limited publish-rejection diagnostics (kind and fixed reason
  only; no message content, tags, event IDs, user pubkeys or keys).
- [ ] User runs sudo sh deploy/prepare-external-armada.sh from uploaded bundle.
- [ ] Verify public discovery after installation and test the external website
  and installed app with admin/new identities. No agent sends user messages.
- [ ] Resolve installed-app send failure from actual rejection evidence. If it
  recurs, immediately collect: sudo journalctl -u bitcoinwalk-chat-staging
  --since '5 minutes ago' --no-pager -g 'chat publish rejected'

## Stage 2: cut over only after the tests above

- [ ] Serve the standalone page from its own small directory; / and
  /login-test.html point to /join-chat. Keep WSS root, NIP-11 and /healthz.
- [ ] Replace old /s/... application routes with external Armada redirects.
- [ ] Remove catch-all Armada static hosting and /signer proxy route. Validate
  and back up Caddy first. No legacy hostname or organizer relay changes.
- [ ] Stop/disable bitcoinwalk-signer-staging after confirming no test login
  depends on it. Existing hosted-browser pairings using it will stop working.
- [ ] Move installed Armada assets, signer unit/binary and obsolete deployment
  bundles into a protected rollback archive; verify before deleting anything.
- [ ] Remove signer/hosted-chooser startup code, tests specific to retired
  components and obsolete deploy scripts in a separately tested relay build.
  Keep relay metadata/naming/membership/moderation and backup tooling.
- [ ] Retire local Armada customizations without touching upstream or pushing;
  preserve a patch/archive for traceability, including currently uncommitted work.
- [ ] Final WSS/HTTP, open-join, private read, send and moderation checks.

No stage-2 removal has occurred. Current hosted app and signer are temporary
fallbacks, not part of the final design. Retain database/key backups until the
user separately authorizes their deletion.
