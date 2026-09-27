import {RELAY, ADMIN, diagnostic, assertSigned, assertAuth} from './protocol.mjs';
const button = document.querySelector('#run');
const status = document.querySelector('#status');
const log = text => { status.textContent += `${text}\n`; };

button.addEventListener('click', async () => {
  button.disabled = true;
  status.textContent = '';
  let publisher, reader;
  try {
    if (!window.nostr?.getPublicKey || !window.nostr?.signEvent) throw new Error('No Nostr extension found. Open this URL in the browser where your signing extension is installed.');
    const key = await window.nostr.getPublicKey();
    if (key !== ADMIN) throw new Error('Select the BitcoinWalk super-admin identity in your extension, then retry. The selected identity is not authorized.');
    log('Correct admin identity selected.');
    const {SimplePool, verifyEvent} = window.NostrTools;
    const template = diagnostic(Math.floor(Date.now()/1000), crypto.randomUUID());
    log('Approve the diagnostic event in your extension.');
    const signed = assertSigned(template, await window.nostr.signEvent(structuredClone(template)), verifyEvent);
    log(`Diagnostic event ID: ${signed.id}`);
    publisher = new SimplePool();
    await Promise.all(publisher.publish([RELAY], signed, {
      maxWait: 120000,
      onauth: async template => {
        assertAuth(template);
        log('Approve the staging relay authentication request in your extension.');
        return assertSigned(template, await window.nostr.signEvent(structuredClone(template)), verifyEvent);
      }
    }));
    log('PASS: staging relay acknowledged the signed event.');
    publisher.close([RELAY]);
    publisher.destroy();
    publisher = null;
    reader = new SimplePool();
    log('Reading from a new connection without authentication…');
    await new Promise((resolve, reject) => {
      let found = false;
      reader.subscribeEose([RELAY], {ids: [signed.id], limit: 1}, {
        maxWait: 15000,
        onevent(event) {
          if (event.id === signed.id && verifyEvent(event) && event.pubkey === ADMIN) found = true;
        },
        onclose(reasons) {
          if (found) resolve();
          else reject(new Error(`Publish was accepted but public read-back failed: ${reasons.join('; ')}`));
        }
      });
    });
    log('PASS: the signed diagnostic was retrieved publicly without authentication.');
    log('Test complete. Share these results with me. The diagnostic requests expiry after ten minutes.');
  } catch (error) {
    log(`STOP: ${error.message || error}`);
    log('No production relay or web-app configuration was changed. If publishing was acknowledged above, that diagnostic may still exist on staging until expiry.');
  } finally {
    for (const pool of [publisher, reader]) if (pool) { pool.close([RELAY]); pool.destroy(); }
    button.disabled = false;
  }
});
