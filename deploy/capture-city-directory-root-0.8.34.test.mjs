import assert from "node:assert/strict";
import test from "node:test";

import { captureDirectoryRoot } from "./capture-city-directory-root-0.8.34.mjs";

const eventID = "d16d969d0bf77c1a71453005b551ab7e2693d293888c2119928df93a69b21a79";

class FakeWebSocket {
  static frames = [];
  #listeners = new Map();

  constructor(url) {
    this.url = url;
    queueMicrotask(() => this.#emit("open", {}));
  }

  addEventListener(name, listener) { this.#listeners.set(name, listener); }
  send(raw) {
    const frame = JSON.parse(raw);
    FakeWebSocket.frames.push(frame);
    if (frame[0] !== "REQ") return;
    const event = { id: eventID, kind: 30309, content: "signed content" };
    queueMicrotask(() => this.#emit("message", { data: JSON.stringify(["EVENT", frame[1], event]) }));
  }
  close() {}
  #emit(name, event) { this.#listeners.get(name)?.(event); }
}

test("captures one exact directory root and closes its subscription", async () => {
  FakeWebSocket.frames = [];
  const event = await captureDirectoryRoot({ WebSocketImpl: FakeWebSocket, relayURL: "wss://relay.example/", eventID, timeoutMs: 100 });
  assert.equal(event.id, eventID);
  assert.deepEqual(FakeWebSocket.frames[0], ["REQ", `bitcoinwalk-directory-${eventID.slice(0, 16)}`, { ids: [eventID], kinds: [30309], limit: 1 }]);
  assert.deepEqual(FakeWebSocket.frames.at(-1), ["CLOSE", `bitcoinwalk-directory-${eventID.slice(0, 16)}`]);
});

test("rejects an invalid relay and event ID before connecting", async () => {
  assert.throws(() => captureDirectoryRoot({ WebSocketImpl: FakeWebSocket, relayURL: "https://relay.example/", eventID }), /wss/);
  assert.throws(() => captureDirectoryRoot({ WebSocketImpl: FakeWebSocket, relayURL: "wss://relay.example/", eventID: "bad" }), /event ID/);
});
