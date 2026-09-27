# Organizer permissions — staging 0.2.0

## Scope

This mode must be explicitly enabled using RELAY_ORGANIZER_MODE=true. It replaces the global writer allowlist with per-city, admin-signed authorization. The old foundation binary and original installer are preserved. No production app configuration is switched.

- Any authenticated Nostr identity can submit a valid kind-30303 city proposal. A proposal is not approval or ownership of a city. Proposals for an unregistered city are separated by author; first submission does not grant a monopoly on a city name.
- Kind 30302 is restricted to the super-admin. It establishes the city's creator and editor list. Creator identity is proved by a referenced stored submission. Once registered, only that creator, listed editors and the super-admin can submit revisions for that city ID.
- Authorization content extends the app's current unused schema with creatorPubkey and creatorRevisionId. Existing fields remain cityId, editorPubkeys and superAdminPubkey. The d tag must equal cityId. The editor list must retain the creator, contain 1–100 valid public keys, and every update must be newer than the stored authorization. Creator changes are not implemented.
- Removed editors lose write access immediately; checks are repeated under a shared lock at the storage boundary. Authorizations persist in the event database across restarts. Expiration and deletion of workflow events are blocked.
- Kind 30304 approvals/rejections/revocations require the super-admin. City ID, revision ID and tags must match. Approval requires the revision author to be on the city list (or the admin). Existing decisions can be revoked even if their replaceable revision is no longer retained.
- Reads remain public; **pending proposals are public too**. Never put private details in a city description or meeting information.
- Only kinds 30302, 30303 and 30304 are enabled in organizer mode. Calendar/profile publication, deletion and the old expiring admin diagnostic are deliberately disabled until bound to approved revisions. Other relays and the production site are unaffected.

## Boundaries and remaining work

- This implements relay authorization, not a complete organizer UI or automatic approvals. City-name/slug collisions need admin review; an unrelated city ID cannot inherit another city's grants.
- Authenticated public submissions can be spammed. Existing message/query/IP limits remain, but quotas, moderation and stronger connection limits are required before production opening.
- The web app uses addressable kind 30303 with d=cityId, so a new revision from the same author replaces the old revision under NIP-01. A revision-history/approved-snapshot design is still needed before app integration. This change does not silently alter Nostr replacement semantics.
- The app's authorization schema/builder must be extended before it emits grants. It must also resolve the latest approval decision (including revoked) correctly before being switched to staging.
- Do not rerun the old admin diagnostic: its expiration tag and non-city payload are intentionally rejected in organizer mode.

## Verified locally

Tests cover matching NIP-42 identity, new proposals, admin-only editor lists and decisions, forged creator, cross-city approval, other-city edits, creator retention, editor removal, stale-grant replay, storage-boundary revocation checks, restart persistence, malformed tags/documents, future timestamps, and authenticated WebSocket submissions. The baseline relay tests still pass. Test identities are generated solely for tests; no user private keys are used.

## Staging deployment

Run on the VPS after the prepared bundle is uploaded:

```sh
cd /home/bitcoinwalk/bitcoinwalk-relay-organizers-0.2.0
sudo sh deploy/upgrade-organizers.sh
```

The installer checks bundle hashes and the exact existing foundation binary, stops the relay briefly, copies its database and binary to a protected backup directory, installs the new binary and organizer-mode drop-in, then starts and checks it. On failure it restores the old binary/drop-in state and preserves the database. It does not modify Caddy or DNS. Human organizer testing remains pending after installation.
