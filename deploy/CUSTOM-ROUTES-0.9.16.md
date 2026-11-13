# AllTrails custom-route relay update

Adds the AllTrails `/explore/custom-routes/<slug>` share-link format to the
existing signed calendar route checks. HTTPS, exact host, route marker,
author permissions, and approval validation remain required.

The prepared production binary was built from
`cadf83d5bfda79af4b75131ab49f5e61c0840f18` plus only the `calendar.go` and
`calendar_test.go` custom-route changes. It intentionally excludes the newer
automation-signer changes in this development branch.

Full `go test ./...` and `go vet ./...` passed on that isolated release source.
The binary SHA-256 is
`8de1f975ebfb5d32a2cbf74c546d40e5bc1e8efe9fb74044ecc885f236a47750`.
The verified package is at
`/home/bitcoinwalk/releases/alltrails-custom-routes-0.9.16` on Relays.
Activation requires the guarded sudo installer; preparation did not activate it.

Do not rebuild this production package from the development branch tip: doing
so would include unrelated changes. Keep the recorded base and narrow patch.
