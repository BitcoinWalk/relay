export const RELAY = 'wss://relay-staging.bitcoinwalk.org';
export const ADMIN = '90cf043861e5b5a9972cb7b529a5ba71b215d6d1e314c749d5526ec133f1db73';

export function diagnostic(now, id) {
  return {kind: 30303, created_at: now, tags: [['d', `bitcoinwalk-diagnostic-${id}`], ['expiration', String(now + 600)]],
    content: JSON.stringify({type: 'bitcoinwalk-relay-diagnostic', message: 'Admin staging connectivity test; not a city revision or approval.'})};
}

export function assertSigned(template, event, verify) {
  if (event.pubkey !== ADMIN || !verify(event)) throw new Error('Signer returned an invalid signature or a different identity. Nothing further was sent.');
  for (const field of ['kind', 'created_at', 'tags', 'content']) {
    if (JSON.stringify(template[field]) !== JSON.stringify(event[field])) throw new Error(`Signer changed ${field}; refusing to send.`);
  }
  return event;
}

export function assertAuth(template) {
  const relayTags = template.tags.filter(tag => tag[0] === 'relay');
  if (template.kind !== 22242 || template.content !== '' || relayTags.length !== 1 || relayTags[0][1].replace(/\/$/, '') !== RELAY || !template.tags.some(tag => tag[0] === 'challenge' && tag[1])) {
    throw new Error('Unexpected authentication request; refusing to sign.');
  }
}

export function editorGrantUpdate(event, cityId, creator, editor, allow, timestamp, verify) {
  if (!/^[0-9a-f]{64}$/.test(editor) || editor === ADMIN || editor === creator) throw new Error('Use a third identity, distinct from the creator and super-admin.');
  if (event.kind !== 30302 || event.pubkey !== ADMIN || !verify(event)) throw new Error('Invalid admin authorization signature.');
  const tags = event.tags.filter(tag => tag[0] === 'd');
  const grant = JSON.parse(event.content);
  if (tags.length !== 1 || tags[0][1] !== cityId || grant.cityId !== cityId || grant.creatorPubkey !== creator || grant.superAdminPubkey !== ADMIN || !Array.isArray(grant.editorPubkeys) || !grant.editorPubkeys.includes(creator)) throw new Error('Authorization does not match the test city and creator.');
  const editors = new Set(grant.editorPubkeys);
  if (allow) editors.add(editor); else editors.delete(editor);
  if (editors.size > 100) throw new Error('Editor list limit exceeded.');
  const created_at = Math.max(timestamp, event.created_at + 1);
  if (created_at > timestamp + 60) throw new Error('Authorization timestamp is ahead of the clock; wait before retrying.');
  return {kind: 30302, created_at, tags: [['d', cityId]], content: JSON.stringify({...grant, editorPubkeys: [...editors]})};
}
