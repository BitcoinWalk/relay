# Persistent chat staging preparation

Status: local implementation and deployment templates only. User approved
`chat-staging.bitcoinwalk.org`. No VPS files, keys, DNS or running services
changed. Never replace legacy `chat.bitcoinwalk.org`, apex/www,
wildcard records or the organizer staging relay.

After the user's DNS change, all three authoritative Njalla servers and the
local recursive resolver return A 213.232.235.138 for chat-staging; no AAAA or
CNAME records returned. Legacy chat, apex and www remain on 213.232.235.240.
The user made the record change; no registrar mutation was performed by the agent.
VPS has 36 GB
free, existing Caddy and organizer relay active, and port 3335 unoccupied.
Existing Caddyfile serves only relay-staging.bitcoinwalk.org -> 127.0.0.1:3334.

Prepared `deploy/Caddyfile.chat-staging` as an isolated site block, NOT a
replacement Caddyfile; it requires local/server Caddy validation before use.
Native WebSocket and NIP-11 requests route to the persistent relay, other routes
serve the public Armada build. The join entry returns 503 until the group and
chooser are ready. No configuration was uploaded or activated.

## Implemented

- Explicit `RELAY_CHAT_MODE=staging`: separate database and loopback port 3335.
  Requires absolute DB/credential paths and an explicit wss origin. No temporary
  keys, automatic identity reset or automatic group creation in this mode.
- `RELAY_CHAT_KEY_INIT=/absolute/new/path` creates a new 0600 service key without
  printing it; exclusive creation refuses overwrites. Credential loading rejects
  symlinks, nonregular files, group/world permissions and invalid key material.
  Use a private parent directory. The credential is a relay key, never a user nsec.
- Unit template `deploy/bitcoinwalk-chat-staging.service` uses LoadCredential,
  a dedicated StateDirectory and separate binary path. It cannot start until
  an operator installs the key and `/etc/bitcoinwalk-chat-staging/public.conf`.
- Startup replays original signed commands into a private temporary database,
  comparing derived membership/bans, audit/message indexes and roster metadata.
  Verifies projection signatures/source references. Failure stops startup; there
  is no automatic repair or fallback to fresh state. This is not yet an exhaustive
  power-loss/fault-injection or standard-ledger migration certification.
- Bounded in-memory rate-limit map (4096 scopes): 60 connections/IP/minute,
  240 queries/IP/minute, 60 writes/key/minute, 10 joins/key/minute and at most
  16 tracked subscriptions/connection on every query path. Counters reset on
  restart. Existing connection policy also applies. Multi-identity/distributed
  abuse still needs aggregate resource controls and monitoring.

## Backup and restore procedure (operator runbook; not executed on VPS)

1. Schedule a short staging write interruption and stop only the chat-staging
   service. Record binary hash, configuration, key public identity and DB path.
2. As an authorized operator, invoke the installed binary with absolute
   `RELAY_CHAT_DB`, `RELAY_CHAT_KEY_FILE` and a NEW `RELAY_CHAT_BACKUP` path.
   This maintenance mode requires an existing DB, obtains its lock, runs recovery
   verification, then writes and fsyncs a consistent 0600 snapshot. Existing
   backup files are never overwritten. A failure may leave a partial snapshot;
   quarantine it and do not use it as a restore source.
3. Back up the original relay-key file separately into protected storage, along
   with binary/configuration. Never paste it into chat or commit it. A database
   backup alone cannot restore the relay's signed group identity.
4. Restart staging and check health, NIP-11 self, authenticated membership and
   denied access for a banned test identity.
5. Rehearse restore into an isolated private directory/listener using the SAME
   relay key. Verify message counts/content, metadata IDs, memberships and bans
   before approving the backup. Never restore over the active DB. Retain original
   artifacts and agree retention/off-VPS encrypted backup storage before go-live.

Local automated restore test passed: a banned identity remains banned, messages
and all four signed metadata events retain their IDs, and relay identity matches.
An intentional unaudited authority change is detected by recovery verification.

## Remaining gates before public exposure

- Confirm hostname; inspect DNS and VPS resources read-only before preparing
  an exact-host-only Caddy configuration and installer with backups/rollback.
- Protect forwarded IP headers at the proxy. Serve HTTPS/WSS and a staged
  Armada build with explicit public relay settings; don't ship localhost URLs.
- Human super-admin signs initial 9007 group creation. Do not generate or retain
  an organizational private key on the server. Add the chooser only when the
  intended group and metadata have been verified.
- Metadata collision handling now retries exactly once at the next second in
  the transport write path, with cancellation. Only the specific rolled-back
  metadata-budget error is retried; authorization/storage failures are not.
  Persistent exhaustion still returns a rate-limit error. Full load testing
  remains required; this is not a general storage-error retry mechanism.
- Test identity switching, slow clients, replay under crashes, disk/resource
  exhaustion and backup restoration on the actual VPS. Set disk alerts/limits.
- Verify Android app opening and browser/iOS fallback with real devices before
  enabling the native-app button. Legacy chat stays running throughout.

Local checks: full `go test -race ./...` passed on the latest full run; new
recovery/backup tests and `go vet ./...` passed after the final additions.
