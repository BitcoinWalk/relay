# Chat staging web release

Armada source: 5b99f88d309052abc1eeb4f0b2ef437de086e709 (unmodified).
Public build: platform/app/search relays point to wss://chat-staging.bitcoinwalk.org.
Default Blossom list explicitly disabled with comma sentinel (an empty setting
would restore upstream defaults); Concord AV, DM voice, DM and Git discovery
defaults disabled. This does not promise zero external requests: user-configured
services, remote profile/media URLs and other Armada features need separate review.
Profiles/lists on the restricted chat relay are not supported in this iteration.
Use a browser extension signer for the initial staging test; don't import an nsec.

The web bundle omits upstream .well-known app-link declarations because those
belong to armada.buzz, not this host. Native-app opening remains a separate test.

Run sudo sh deploy/install-chat-web.sh from the extracted bundle. It refuses an
existing web target or any change to the independently checked Caddyfile, validates
the combined configuration and keeps a root-only backup. Only the new chat-staging
host is added. Caddy reload failure restores the original config; static files are
retained for diagnosis. Certificate issuance and public HTTP/WSS checks follow.
Never rerun blindly after failure: the retained target deliberately blocks overwrite.

Group provisioning requires the human super-admin to sign kind 9007 in their
extension. No administrator private key is generated or retained on this VPS.
The /join-chat route deliberately remains 503 pending provisioning and checks.
