# Isolated signing transport

Public endpoint: wss://chat-staging.bitcoinwalk.org/signer, separate process
on 127.0.0.1:3336. No new DNS, no chat storage/credential access. Only kind
24133 with one recipient is accepted; signed envelopes are bounded and recent.
Recipient-specific subscriptions do NOT authenticate readers: confidentiality
depends on NIP-46 endpoint encryption. The relay sees IPs, timing and envelope
public keys, not the encrypted request contents or users' private keys.
No persistent messages or relay identity key. This is live forwarding only;
offline delivery is not promised. Rate and subscription limits are in-memory;
restart resets them. Memory/FD/task limits constrain the separate process.

Armada has a local explicit VITE_SIGNING_RELAYS override; unset builds preserve
upstream behavior. Explicit empty/invalid values fail closed, and explicit URLs
replace app/platform fallback selection. Existing pairing sessions retain their
original relays; cancel the failed mobile attempt and start a fresh pairing.
Signer-advertised relay changes and user-entered bunker URIs keep upstream
behavior; the override controls app-initiated pairing only.

Verification: full Go race suite and vet pass, including unsigned-connection
delivery of signed kind 24133, rejection of chat/other kinds, invalid envelope
checks and no stored history. Armada typecheck/lint passed (five existing
warnings). Full suite: 1504 passed, one untouched GifPicker attribution test
failed; targeted new tests pass and production public build succeeds. No
claim of completed Amber/device testing, independent security audit, load
test or crash/recovery certification. Systemd unit validation is performed
on the VPS because local sandbox prevents systemd-analyze initialization.

Install bundle with sudo sh deploy/install-signer.sh. It checks the exact
existing Caddy hash, validates the candidate, starts the separate service,
retains the old web directory and restores it/configuration on installation
failure. Partial files are retained for diagnosis; do not blindly rerun.
Legacy hostnames and chat binaries/key/database are untouched. Public /signer
WSS and actual Amber login must be checked after installation. Use a test
identity, never send an nsec or pairing secret in chat.

Protocol reference: https://github.com/nostr-protocol/nips/blob/master/46.md
