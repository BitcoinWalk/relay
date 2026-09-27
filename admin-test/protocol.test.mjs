import test from 'node:test';
import assert from 'node:assert/strict';
import {ADMIN, RELAY, diagnostic, assertSigned, assertAuth, editorGrantUpdate} from './protocol.mjs';
test('diagnostic is not a calendar event or approval and expires', () => {
  const event = diagnostic(1000, 'test');
  assert.equal(event.kind, 30303);
  assert.deepEqual(event.tags[1], ['expiration', '1600']);
  assert.equal(JSON.parse(event.content).type, 'bitcoinwalk-relay-diagnostic');
});
test('rejects signer identity, signature and payload changes', () => {
  const template = diagnostic(1000, 'test');
  const signed = {...template, pubkey: ADMIN};
  assert.equal(assertSigned(template, signed, () => true), signed);
  assert.throws(() => assertSigned(template, {...signed, pubkey:'other'}, () => true));
  assert.throws(() => assertSigned(template, signed, () => false));
  assert.throws(() => assertSigned(template, {...signed, content:'changed'}, () => true));
});
test('only signs staging authentication challenges', () => {
  const auth = {kind:22242, content:'', tags:[['relay',RELAY],['challenge','test']]};
  assert.doesNotThrow(() => assertAuth(auth));
  assert.throws(() => assertAuth({...auth, kind:1}));
  assert.throws(() => assertAuth({...auth, tags:[['relay','wss://other.example'],['challenge','test']]}));
});

const creator='1'.repeat(64), editor='2'.repeat(64), other='3'.repeat(64), cityId='test-city';
function authorization(editors=[creator,other]) {
  return {kind:30302,pubkey:ADMIN,created_at:1000,tags:[['d',cityId]],content:JSON.stringify({cityId,creatorPubkey:creator,creatorRevisionId:'4'.repeat(64),editorPubkeys:editors,superAdminPubkey:ADMIN})};
}
test('grant adds only selected editor and preserves creator and other editors',()=>{
 const original=authorization();const before=JSON.stringify(original);
 const update=editorGrantUpdate(original,cityId,creator,editor,true,1000,()=>true);
 const grant=JSON.parse(update.content);
 assert.deepEqual(grant.editorPubkeys,[creator,other,editor]);
 assert.equal(grant.creatorRevisionId,'4'.repeat(64));
 assert.equal(update.created_at,1001);
 assert.equal(JSON.stringify(original),before);
 assert.deepEqual(JSON.parse(editorGrantUpdate({...original,content:update.content},cityId,creator,editor,true,1001,()=>true).content).editorPubkeys,[creator,other,editor]);
});
test('revocation removes only selected collaborator',()=>{
 const update=editorGrantUpdate(authorization([creator,editor,other]),cityId,creator,editor,false,1001,()=>true);
 assert.deepEqual(JSON.parse(update.content).editorPubkeys,[creator,other]);
});
test('grant changes fail closed on incorrect authority, city or creator',()=>{
 for(const key of [creator,ADMIN,'bad'])assert.throws(()=>editorGrantUpdate(authorization(),cityId,creator,key,true,1001,()=>true));
 assert.throws(()=>editorGrantUpdate(authorization(),cityId,creator,editor,true,1001,()=>false));
 assert.throws(()=>editorGrantUpdate({...authorization(),pubkey:other},cityId,creator,editor,true,1001,()=>true));
 assert.throws(()=>editorGrantUpdate(authorization(),'other-city',creator,editor,true,1001,()=>true));
 assert.throws(()=>editorGrantUpdate(authorization(),cityId,other,editor,true,1001,()=>true));
 assert.throws(()=>editorGrantUpdate(authorization([other]),cityId,creator,editor,true,1001,()=>true));
 assert.throws(()=>editorGrantUpdate(authorization(),cityId,creator,editor,true,900,()=>true));
});
