#!/usr/bin/env node

import { pathToFileURL } from "node:url";

const DIRECTORY_KIND = 30309;
const HEX_32 = /^[0-9a-f]{64}$/;

export function captureDirectoryRoot({ WebSocketImpl = globalThis.WebSocket, relayURL, eventID, timeoutMs = 15_000 }) {
  if (typeof WebSocketImpl !== "function") throw new Error("WebSocket support is unavailable");
  if (!HEX_32.test(eventID)) throw new Error("event ID must be 32-byte lowercase hex");
  const parsed = new URL(relayURL);
  const loopbackWS = parsed.protocol === "ws:" && (parsed.hostname === "127.0.0.1" || parsed.hostname === "[::1]");
  if ((parsed.protocol !== "wss:" && !loopbackWS) || parsed.username || parsed.password || parsed.search || parsed.hash) {
    throw new Error("relay must be an uncredentialed wss URL or loopback ws URL");
  }

  return new Promise((resolve, reject) => {
    const subscription = `bitcoinwalk-directory-${eventID.slice(0, 16)}`;
    const socket = new WebSocketImpl(parsed.href);
    let event;
    let settled = false;
    const finish = (error) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      try { socket.send(JSON.stringify(["CLOSE", subscription])); } catch {}
      try { socket.close(); } catch {}
      if (error) reject(error); else resolve(event);
    };
    const timer = setTimeout(() => finish(new Error("relay read timed out")), timeoutMs);

    socket.addEventListener("open", () => {
      socket.send(JSON.stringify(["REQ", subscription, { ids: [eventID], kinds: [DIRECTORY_KIND], limit: 1 }]));
    });
    socket.addEventListener("message", ({ data }) => {
      if (typeof data !== "string" || Buffer.byteLength(data) > 65_536) return finish(new Error("invalid relay message"));
      let message;
      try { message = JSON.parse(data); } catch { return finish(new Error("invalid relay JSON")); }
      if (!Array.isArray(message) || message.length < 2) return finish(new Error("invalid relay frame"));
      if (message[0] === "EVENT" && message[1] === subscription) {
        const candidate = message[2];
        if (!candidate || candidate.id !== eventID || candidate.kind !== DIRECTORY_KIND) {
          return finish(new Error("relay returned an unexpected directory event"));
        }
        event = candidate;
        return finish();
      } else if (message[0] === "EOSE" && message[1] === subscription) {
        return finish(new Error("directory root was not found"));
      } else if (message[0] === "CLOSED" && message[1] === subscription) {
        return finish(new Error("relay closed the directory subscription"));
      }
    });
    socket.addEventListener("error", () => finish(new Error("relay connection failed")));
    socket.addEventListener("close", () => {
      if (!settled) finish(new Error("relay closed before EOSE"));
    });
  });
}

async function main() {
  if (process.argv.length !== 4) throw new Error(`Usage: ${process.argv[1]} RELAY_WSS EVENT_ID`);
  const event = await captureDirectoryRoot({ relayURL: process.argv[2], eventID: process.argv[3] });
  process.stdout.write(`${JSON.stringify({ version: 1, events: [event] })}\n`);
}

if (process.argv[1] && pathToFileURL(process.argv[1]).href === import.meta.url) {
  main().catch((error) => {
    process.stderr.write(`${error.message}\n`);
    process.exitCode = 1;
  });
}
