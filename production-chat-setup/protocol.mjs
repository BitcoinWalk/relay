export const RELAY = "wss://chat.bitcoinwalk.org";
export const ADMIN = "90cf043861e5b5a9972cb7b529a5ba71b215d6d1e314c749d5526ec133f1db73";

export function randomGroupId(bytes) {
  if (!(bytes instanceof Uint8Array) || bytes.length !== 8) throw new Error("Eight random bytes are required.");
  return [...bytes].map(value => value.toString(16).padStart(2, "0")).join("");
}

export function creation(groupId, now) {
  if (!/^[0-9a-f]{16}$/.test(groupId)) throw new Error("Invalid production group ID.");
  return {kind: 9007, created_at: now, tags: [["h", groupId], ["client", "bitcoinwalk-production-setup"]], content: "Create the BitcoinWalk global community"};
}

export function metadata(groupId, now) {
  if (!/^[0-9a-f]{16}$/.test(groupId)) throw new Error("Invalid production group ID.");
  return {
    kind: 9002,
    created_at: now,
    tags: [
      ["h", groupId],
      ["name", "chat"],
      ["about", "BitcoinWalk global community. Anyone can join; only joined members can read messages."],
      ["banner", ""],
      ["private"],
      ["visibility", "private"],
      ["open"],
      ["client", "bitcoinwalk-production-setup"],
    ],
    content: "",
  };
}

export function assertSigned(template, event, verify) {
  if (event.pubkey !== ADMIN || !verify(event)) throw new Error("The signer returned an invalid signature or a different identity.");
  for (const field of ["kind", "created_at", "tags", "content"]) {
    if (JSON.stringify(template[field]) !== JSON.stringify(event[field])) throw new Error(`The signer changed ${field}; refusing to publish.`);
  }
  return event;
}

export function assertAuth(template) {
  const relays = template.tags.filter(tag => tag[0] === "relay");
  const challenges = template.tags.filter(tag => tag[0] === "challenge");
  if (template.kind !== 22242 || template.content !== "" || relays.length !== 1 || challenges.length !== 1 || !challenges[0][1] || relays[0][1].replace(/\/$/, "") !== RELAY) {
    throw new Error("Unexpected relay-authentication request; refusing to sign.");
  }
}

export function armadaUrl(groupId) {
  if (!/^[0-9a-f]{16}$/.test(groupId)) throw new Error("Invalid production group ID.");
  return `https://armada.buzz/s/chat.bitcoinwalk.org/${groupId}`;
}
