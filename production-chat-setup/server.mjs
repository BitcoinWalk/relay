import http from "node:http";
import {readFile} from "node:fs/promises";

const port = 3345;
const files = new Map([
  ["/", [new URL("./index.html", import.meta.url), "text/html"]],
  ["/setup.mjs", [new URL("./setup.mjs", import.meta.url), "text/javascript"]],
  ["/protocol.mjs", [new URL("./protocol.mjs", import.meta.url), "text/javascript"]],
  ["/nostr.bundle.js", ["/home/endo/Documents/Codex/BitcoinWalk-web/node_modules/nostr-tools/lib/nostr.bundle.js", "text/javascript"]],
]);

http.createServer(async (request, response) => {
  if (![`127.0.0.1:${port}`, `localhost:${port}`].includes(request.headers.host)) { response.writeHead(403).end(); return; }
  const entry = files.get(request.url);
  if (request.method !== "GET" || !entry) { response.writeHead(404).end(); return; }
  try {
    const content = await readFile(entry[0]);
    response.writeHead(200, {
      "Content-Type": `${entry[1]}; charset=utf-8`,
      "Cache-Control": "no-store",
      "X-Content-Type-Options": "nosniff",
      "Content-Security-Policy": "default-src 'self'; connect-src wss://chat.bitcoinwalk.org https://chat.bitcoinwalk.org; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'",
    });
    response.end(content);
  } catch {
    response.writeHead(500).end("Unable to load the local setup page.");
  }
}).listen(port, "127.0.0.1", () => console.log(`Production chat setup: http://localhost:${port}`));
