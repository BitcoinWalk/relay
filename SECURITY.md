# Security policy

Do not open a public issue containing a private key, credential, unpublished
event, private message, host access detail or exploitable vulnerability.

Report vulnerabilities privately through the GitHub repository's security
advisory interface. Include the affected commit, a minimal reproduction and the
expected impact. Never test against BitcoinWalk production infrastructure or
other users' relays without explicit authorization.

No Bitcoin, Lightning, Nostr signing or replication secret belongs in this
repository. If a secret is committed, treat it as compromised and rotate it;
removing it from later Git history is not sufficient.
