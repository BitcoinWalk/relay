import test from "node:test";
import assert from "node:assert/strict";
import {ADMIN, RELAY, armadaUrl, assertAuth, assertSigned, creation, metadata, randomGroupId} from "./protocol.mjs";

test("creates a stable 128-bit-looking group identifier from eight bytes", () => {
  assert.equal(randomGroupId(new Uint8Array([0, 1, 2, 3, 252, 253, 254, 255])), "00010203fcfdfeff");
  assert.throws(() => randomGroupId(new Uint8Array(7)));
});

test("requests open joining and private member visibility", () => {
  assert.deepEqual(creation("0123456789abcdef", 100).tags[0], ["h", "0123456789abcdef"]);
  const tags = metadata("0123456789abcdef", 101).tags;
  assert(tags.some(tag => tag[0] === "open"));
  assert(tags.some(tag => tag[0] === "private"));
  assert(tags.some(tag => tag[0] === "visibility" && tag[1] === "private"));
});

test("accepts only the production relay auth challenge", () => {
  const auth = {kind: 22242, content: "", tags: [["relay", RELAY], ["challenge", "value"]]};
  assert.doesNotThrow(() => assertAuth(auth));
  assert.throws(() => assertAuth({...auth, tags: [["relay", "wss://other.example"], ["challenge", "value"]]}));
});

test("rejects a changed signer identity or payload", () => {
  const template = creation("0123456789abcdef", 100);
  const signed = {...template, pubkey: ADMIN};
  assert.equal(assertSigned(template, signed, () => true), signed);
  assert.throws(() => assertSigned(template, {...signed, content: "changed"}, () => true));
  assert.throws(() => assertSigned(template, {...signed, pubkey: "0".repeat(64)}, () => true));
});

test("constructs the official Armada route", () => {
  assert.equal(armadaUrl("0123456789abcdef"), "https://armada.buzz/s/chat.bitcoinwalk.org/0123456789abcdef");
});
