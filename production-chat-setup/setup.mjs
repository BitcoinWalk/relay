import {ADMIN, RELAY, armadaUrl, assertAuth, assertSigned, creation, metadata, randomGroupId} from "./protocol.mjs";

const button = document.querySelector("#create");
const status = document.querySelector("#status");
const storageKey = "bitcoinwalk-production-chat-setup-v1";
const log = line => { status.textContent += `${line}\n`; };
const now = () => Math.floor(Date.now() / 1000);

async function sign(template) {
  const event = await window.nostr.signEvent(structuredClone(template));
  return assertSigned(template, event, window.NostrTools.verifyEvent);
}

async function publish(template) {
  const event = await sign(template);
  const pool = new window.NostrTools.SimplePool();
  try {
    await Promise.all(pool.publish([RELAY], event, {
      maxWait: 120000,
      onauth: async auth => {
        assertAuth(auth);
        log("Approve relay authentication in your extension.");
        return sign(auth);
      },
    }));
    return event;
  } finally {
    pool.close([RELAY]);
    pool.destroy();
  }
}

async function verifyMetadata(groupId) {
  const pool = new window.NostrTools.SimplePool();
  try {
    const events = await pool.querySync([RELAY], {kinds: [39000], "#d": [groupId], limit: 1}, {maxWait: 15000});
    const event = events.find(item => window.NostrTools.verifyEvent(item) && item.tags.some(tag => tag[0] === "d" && tag[1] === groupId));
    if (!event || !event.tags.some(tag => tag[0] === "name" && tag[1] === "chat") || !event.tags.some(tag => tag[0] === "private") || !event.tags.some(tag => tag[0] === "restricted")) {
      throw new Error("The relay did not return the expected signed group metadata.");
    }
  } finally {
    pool.close([RELAY]);
    pool.destroy();
  }
}

button.addEventListener("click", async () => {
  button.disabled = true;
  status.textContent = "";
  try {
    if (!window.nostr?.getPublicKey || !window.nostr?.signEvent) throw new Error("Open this page in the browser containing your Nostr signing extension.");
    const key = await window.nostr.getPublicKey();
    if (key !== ADMIN) throw new Error("Select the BitcoinWalk super-admin identity, then retry.");
    log("Correct super-admin identity selected.");
    let saved = JSON.parse(localStorage.getItem(storageKey) || "null");
    if (!saved) {
      const bytes = new Uint8Array(8);
      crypto.getRandomValues(bytes);
      saved = {groupId: randomGroupId(bytes), created: false, named: false};
      localStorage.setItem(storageKey, JSON.stringify(saved));
    }
    if (!saved.created) {
      log(`Approve creation of production group ${saved.groupId}.`);
      const event = await publish(creation(saved.groupId, now()));
      saved.created = true;
      saved.creationEvent = event.id;
      localStorage.setItem(storageKey, JSON.stringify(saved));
      log("Group creation acknowledged.");
    }
    if (!saved.named) {
      log("Approve the #chat name, open joining and members-only visibility.");
      const event = await publish(metadata(saved.groupId, now()));
      saved.named = true;
      saved.metadataEvent = event.id;
      localStorage.setItem(storageKey, JSON.stringify(saved));
      log("Group metadata acknowledged.");
    }
    await verifyMetadata(saved.groupId);
    const url = armadaUrl(saved.groupId);
    log("PASS: production group metadata verified.");
    log(`Group ID: ${saved.groupId}`);
    log(`Armada invite: ${url}`);
    log("Share this complete output. Do not create another group.");
  } catch (error) {
    log(`STOP: ${error.message || error}`);
    log("If group creation was acknowledged, click again to resume metadata rather than creating another group.");
  } finally {
    button.disabled = false;
  }
});
