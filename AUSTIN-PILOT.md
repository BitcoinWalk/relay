# Austin dedicated-relay pilot

User approved a paid-city experience test without real payment. This is not automatic paid provisioning or production rollout.

## Isolation

- Host: `austin-staging.bitcoinwalk.org` on new VPS `213.232.235.138`.
- Service: `bitcoinwalk-austin-staging`, loopback `127.0.0.1:3337`.
- Separate `/opt`, `/etc`, and `/var/lib/bitcoinwalk-austin-staging` namespaces; new private relay key and empty database. No shared global-chat membership or key.
- Reuse the verified chat-delivery binary SHA-256 `8a4a656573ea92f7af9cda3d3fdbfc58d750386bd2e12ad4751b536b8eda7dc4` with hardened DynamicUser unit.
- Existing global chat, organizer, Caddy and legacy hosts untouched by private installer. Installer refuses existing targets; failures stop only the new service and retain files. No automatic destructive retry.

## Gates / progress

- [x] Read-only VPS preflight: existing services active, port 3337 unused, 36 GB free. DNS currently resolves to legacy `.240`.
- [x] All three authoritative Njalla servers return A `213.232.235.138`, AAAA ENODATA for `austin-staging.bitcoinwalk.org`.
- [x] User installed private backend; read-only SSH confirms all four services active and Austin health OK. Austin relay public key `dbb67877549224ddb970658b9a9622d19fa5f45b377fb89e1787d19920cb3c2e` differs from global chat. Protected initial key backup `/var/backups/bitcoinwalk-austin-key.ypsq4i/relay-key`. Hardened service template unchanged.
- [x] User installed additive Caddy host; backup `/var/backups/bitcoinwalk-austin-https.fS1ukD/Caddyfile`. Public trusted HTTPS health/metadata pass, WSS discovery returns EOSE with no groups. Existing global and organizer health pass. Join route intentionally 503 pending group creation.
- [ ] Human super-admin signs creation of a new group and channel name in external Armada. Capture actual group ID; no guessed invite or private-key collection.
- [x] Added isolated local `/pilot/austin` page using synthetic fixture UUID and `austin-staging` slug; verified named channel `d8006bee1ddd5b5f`. Existing `/austin` stays global. Browser confirms dedicated link and staging notice; 31 web tests and build pass. Real city records/payment status unchanged.
- [ ] Test desktop/mobile open join and cross-user history/live delivery; anonymous/nonmember denial; removal/ban and group isolation. Preserve global-chat regression checks.
- [ ] Mark pilot acceptance. Real payment, provisioning automation and production migration remain separate work.
