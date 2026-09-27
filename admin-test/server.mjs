import http from 'node:http';
import {readFile} from 'node:fs/promises';
const organizerMode=process.argv.includes('--organizer');
const port=organizerMode?3341:3340;
const files = new Map([
  ['/', [new URL(organizerMode?'./organizer.html':'./index.html', import.meta.url), 'text/html']],
  ['/organizer.mjs', [new URL('./organizer.mjs', import.meta.url), 'text/javascript']],
  ['/test.mjs', [new URL('./test.mjs', import.meta.url), 'text/javascript']],
  ['/protocol.mjs', [new URL('./protocol.mjs', import.meta.url), 'text/javascript']],
  ['/nostr.bundle.js', ['/home/endo/Documents/Codex/BitcoinWalk-web/node_modules/nostr-tools/lib/nostr.bundle.js', 'text/javascript']],
]);
http.createServer(async (req, res) => {
  if (![`127.0.0.1:${port}`, `localhost:${port}`].includes(req.headers.host)) { res.writeHead(403).end(); return; }
  const entry = files.get(req.url);
  if (req.method !== 'GET' || !entry) { res.writeHead(404).end(); return; }
  try {
    const content = await readFile(entry[0]);
    res.writeHead(200, {'Content-Type': `${entry[1]}; charset=utf-8`, 'Cache-Control':'no-store', 'X-Content-Type-Options':'nosniff', 'Content-Security-Policy': "default-src 'self'; connect-src wss://relay-staging.bitcoinwalk.org https://relay-staging.bitcoinwalk.org; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'"});
    res.end(content);
  } catch { res.writeHead(500).end('Unable to load local test assets.'); }
}).listen(port, '127.0.0.1', () => console.log(`Staging test: http://localhost:${port}`));
