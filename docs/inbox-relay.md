# BitcoinWalk private inbox relay

The BitcoinWalk private inbox is a dedicated NIP-17/NIP-59 transport. It is
deliberately separate from the public event relay and the NIP-29 community
relay. The separation prevents encrypted direct-message gift wraps from being
discoverable through public event or group-chat queries.

## Privacy and access contract

- The relay accepts only kind `1059` gift wraps.
- Every write requires a successful NIP-42 authentication.
- A gift wrap must contain exactly one `p` tag. The only other accepted tag is
  an optional `expiration` tag.
- A read must request kind `1059` for exactly one `#p` recipient. That recipient
  must be the NIP-42-authenticated reader.
- Live delivery is sent only to the authenticated tagged recipient.
- Gift wraps older than 30 days are rejected and stored wraps are pruned hourly.
- The relay stores encrypted gift wraps only. It never receives or stores a
  user's Nostr secret key or decrypted message.

## Rollout

1. Build and install the isolated staging service on loopback port `3348` with
   `deploy/install-inbox-staging-0.9.7.sh`.
2. Point `inbox-staging.bitcoinwalk.org` at the relay server.
3. Publish the staging endpoint with
   `deploy/activate-inbox-staging-https-0.9.7.sh`.
4. Exercise NIP-42 publish, recipient read, foreign-reader rejection, restart,
   and retention acceptance before preparing the production service.
5. Only after staging acceptance, provision `inbox.bitcoinwalk.org`, deploy an
   independent production database, and migrate the app's NIP-17 relay list.

Push notifications are a separate HTTP service. This relay replaces the
message-transport dependency on `relay.psstpsst.chat`; it does not by itself
replace the existing push API.
