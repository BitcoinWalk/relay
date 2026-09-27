import {ADMIN, RELAY, assertAuth, editorGrantUpdate} from './protocol.mjs';
const status=document.querySelector('#status');
const buttons=[...document.querySelectorAll('button')];
const storageKey='bitcoinwalk-staging-organizer-test-v1';
const log=text=>{status.textContent+=`${text}\n`;};
const load=()=>{const value=localStorage.getItem(storageKey);if(!value)throw Error('Complete step 1 in this browser first.');return JSON.parse(value);};
const save=value=>localStorage.setItem(storageKey,JSON.stringify(value));
const now=()=>Math.floor(Date.now()/1000);
async function identity(){if(!window.nostr)throw Error('Open in the browser with your Nostr signing extension.');return window.nostr.getPublicKey();}
async function sign(template,key){
 const event=await window.nostr.signEvent(structuredClone(template));
 if(event.pubkey!==key||!NostrTools.verifyEvent(event))throw Error('Invalid signature or identity changed; refusing to send.');
 for(const field of ['kind','created_at','tags','content'])if(JSON.stringify(event[field])!==JSON.stringify(template[field]))throw Error('Signer changed the requested payload.');
 return event;
}
async function publish(template,key){
 const event=await sign(template,key);const pool=new NostrTools.SimplePool();
 try{await Promise.all(pool.publish([RELAY],event,{maxWait:120000,onauth:async auth=>{assertAuth(auth);log('Approve relay authentication if prompted.');return sign(auth,key);}}));return event;}
 finally{pool.close([RELAY]);pool.destroy();}
}
async function read(filter){
 const pool=new NostrTools.SimplePool();
 try{return await new Promise((resolve,reject)=>{const found=[];pool.subscribeEose([RELAY],filter,{maxWait:15000,onevent:e=>{if(NostrTools.verifyEvent(e))found.push(e);},onclose:()=>found.length?resolve(found):reject(Error('No matching signed event returned by staging.'))});});}
 finally{pool.close([RELAY]);pool.destroy();}
}
function draft(city,time=now()){return {kind:30303,created_at:time,tags:[['d',city.cityId],['city',city.slug],['client','bitcoinwalk-staging-test']],content:JSON.stringify(city)};}
async function checkMode(){const response=await fetch('https://relay-staging.bitcoinwalk.org/',{headers:{Accept:'application/nostr+json'}});if(!response.ok)throw Error('Cannot read staging relay version.');const info=await response.json();if(info.version!=='bitcoinwalk-organizers-0.2.0')throw Error('Install the organizer-mode upgrade first. Staging is still running '+info.version);}
async function run(action){buttons.forEach(b=>b.disabled=true);status.textContent='';try{await checkMode();await action();}catch(e){log(`STOP: ${e.message||e}`);}finally{buttons.forEach(b=>b.disabled=false);}}
document.querySelector('#submit').addEventListener('click',()=>run(async()=>{
 const key=await identity();if(key===ADMIN)throw Error('Select a separate organizer identity for step 1.');
 if(localStorage.getItem(storageKey))throw Error('A test proposal already exists in this browser. Continue with step 2 or 3 rather than creating duplicates.');
 const city={cityId:crypto.randomUUID(),slug:'organizer-test-'+crypto.randomUUID().slice(0,8),cityName:'Organizer permissions test',startAt:new Date(Date.now()+86400000).toISOString(),description:'Unapproved staging-only permission test. Not a real walk.',meetingPoint:{description:'Test location—not a meeting invitation',latitude:0,longitude:0},chatUrl:'https://example.com/test-chat',heroImageUrl:'https://example.com/test-image.jpg'};
 log('Approve the unapproved test proposal in your extension.');
 const event=await publish(draft(city),key);
 save({city,creator:key,originalID:event.id,latestID:event.id,latestTime:event.created_at});
 await read({ids:[event.id],limit:1});
 log('PASS: organizer submitted a draft and it was read publicly without authentication.');log(`City ID: ${city.cityId}\nCreator public key: ${key}\nSubmission: ${event.id}`);log('Now select the super-admin identity and run step 2.');
}));
document.querySelector('#grant').addEventListener('click',()=>run(async()=>{
 const key=await identity();if(key!==ADMIN)throw Error('Select the BitcoinWalk super-admin for step 2.');const saved=load();
 const [original]=await read({ids:[saved.originalID],limit:1});
 if(original.pubkey!==saved.creator||original.kind!==30303||JSON.parse(original.content).cityId!==saved.city.cityId)throw Error('Stored test information does not match the signed relay event.');
 const content={cityId:saved.city.cityId,creatorPubkey:original.pubkey,creatorRevisionId:original.id,editorPubkeys:[original.pubkey],superAdminPubkey:ADMIN};
 log(`Approve an editor grant for test city ${saved.city.cityId}, creator ${original.pubkey}. This is not a walk approval.`);
 const event=await publish({kind:30302,created_at:now(),tags:[['d',saved.city.cityId]],content:JSON.stringify(content)},key);
 await read({ids:[event.id],limit:1});saved.grantID=event.id;save(saved);log('PASS: admin-signed creator authorization stored and publicly readable.');log('Switch back to the organizer and run step 3.');
}));
document.querySelector('#edit').addEventListener('click',()=>run(async()=>{
 const key=await identity();const saved=load();if(key!==saved.creator||key===ADMIN)throw Error('Select the same organizer identity used in step 1.');
 const grants=await read({authors:[ADMIN],kinds:[30302],'#d':[saved.city.cityId],limit:1});
 const grant=JSON.parse(grants[0].content);if(grant.creatorPubkey!==key)throw Error('Admin authorization does not match this creator.');
 saved.city.description='Unapproved staging-only permission test. Organizer edit verified. Not a real walk.';
 const event=await publish(draft(saved.city,Math.max(now(),saved.latestTime+1)),key);saved.latestID=event.id;saved.latestTime=event.created_at;save(saved);
 await read({ids:[event.id],limit:1});log('PASS: authorized organizer edit accepted and read back.');
 log('Approve signing the negative-test approval attempt. The relay must reject it.');
 const approval={kind:30304,created_at:now(),tags:[['d',saved.city.cityId],['e',event.id,'','city-revision'],['status','approved']],content:JSON.stringify({cityId:saved.city.cityId,cityRevisionId:event.id,status:'approved'})};
 let denied=false;
 try{await publish(approval,key);}catch(e){if(String(e.message||e).includes('restricted: only the super-admin can approve'))denied=true;else throw e;}
 if(!denied)throw Error('SECURITY TEST FAILED: organizer approval was unexpectedly accepted. Stop testing and report this.');
 log('PASS: relay rejected organizer self-approval.');log('Organizer workflow verified. Share this output. The test city remains unapproved.');
}));

async function currentGrant(cityId){
 const events=await read({authors:[ADMIN],kinds:[30302],'#d':[cityId],limit:1});
 const event=events[0];const content=JSON.parse(event.content);
 if(event.kind!==30302||event.pubkey!==ADMIN||content.cityId!==cityId||content.superAdminPubkey!==ADMIN)throw Error('Unexpected city authorization.');
 return event;
}
function collaborator(saved){
 if(!saved.collaborator||saved.collaborator===ADMIN||saved.collaborator===saved.creator)throw Error('Choose a separate second editor in step 4 first.');
 return saved.collaborator;
}
async function expectCityDenial(template,key){
 try{await publish(template,key);}catch(e){
  if(String(e.message||e).includes('restricted: you are not an editor of this city'))return;
  throw e; // Signing refusal, network errors and rate limits are NOT proof of denial.
 }
 throw Error('SECURITY TEST FAILED: unauthorized edit was accepted. Stop and report this.');
}
document.querySelector('#choose-editor').addEventListener('click',()=>run(async()=>{
 const saved=load();const key=await identity();
 if(key===ADMIN||key===saved.creator||!/^[0-9a-f]{64}$/.test(key))throw Error('Choose a third identity, distinct from the creator and super-admin.');
 if(saved.collaborator&&saved.collaborator!==key)throw Error('A different second editor was already selected. Switch back to that identity to finish this test.');
 const grant=JSON.parse((await currentGrant(saved.city.cityId)).content);
 if(grant.creatorPubkey!==saved.creator)throw Error('Complete the original organizer authorization before continuing.');
 saved.collaborator=key;save(saved);log(`Second editor selected: ${key}`);log('No access granted yet. Switch to the super-admin and run step 5.');
}));
document.querySelector('#grant-editor').addEventListener('click',()=>run(async()=>{
 const saved=load();const editor=collaborator(saved);const key=await identity();if(key!==ADMIN)throw Error('Select the super-admin for step 5.');
 if(saved.revocationID)throw Error('This test has already reached revocation. Continue at step 8; do not re-grant access.');
 if(!saved.protectedCity){
  saved.protectedCity={...saved.city,cityId:crypto.randomUUID(),slug:'protected-test-'+crypto.randomUUID().slice(0,8),cityName:'Protected editor isolation test',description:'Unapproved staging-only isolation test. Not a real walk.'};save(saved);
 }
 if(!saved.protectedOriginalID){
  log('Approve creating the second, admin-controlled test proposal.');
  const proposal=await publish(draft(saved.protectedCity),key);saved.protectedOriginalID=proposal.id;save(saved);
 }
 if(!saved.protectedGrantID){
  const [original]=await read({ids:[saved.protectedOriginalID],limit:1});
  if(original.pubkey!==ADMIN||original.kind!==30303||JSON.parse(original.content).cityId!==saved.protectedCity.cityId)throw Error('Protected proposal does not match this test.');
  log('Approve registering the protected city with the admin as its only editor.');
  const grant={cityId:saved.protectedCity.cityId,creatorPubkey:ADMIN,creatorRevisionId:original.id,editorPubkeys:[ADMIN],superAdminPubkey:ADMIN};
  const event=await publish({kind:30302,created_at:now(),tags:[['d',grant.cityId]],content:JSON.stringify(grant)},key);
  saved.protectedGrantID=event.id;save(saved);
 }
 const protectedGrant=JSON.parse((await currentGrant(saved.protectedCity.cityId)).content);
 if(protectedGrant.creatorPubkey!==ADMIN||protectedGrant.editorPubkeys.length!==1||protectedGrant.editorPubkeys[0]!==ADMIN)throw Error('Protected city must have only the admin as editor.');
 const template=editorGrantUpdate(await currentGrant(saved.city.cityId),saved.city.cityId,saved.creator,editor,true,now(),NostrTools.verifyEvent);
 log(`Approve adding ${editor} to original test city ${saved.city.cityId} only.`);
 const event=await publish(template,key);await read({ids:[event.id],limit:1});saved.editorGrantID=event.id;save(saved);
 log('PASS: second editor grant stored; protected city remains admin-only.');log('Switch to the second editor and run step 6.');
}));
document.querySelector('#test-editor').addEventListener('click',()=>run(async()=>{
 const saved=load();const key=await identity();if(key!==collaborator(saved))throw Error('Select the second editor identity for step 6.');
 if(!saved.editorGrantID||!saved.protectedGrantID||saved.revocationID)throw Error('Complete step 5 first; this test must run before revocation.');
 const granted=JSON.parse((await currentGrant(saved.city.cityId)).content);
 const protectedGrant=JSON.parse((await currentGrant(saved.protectedCity.cityId)).content);
 if(!granted.editorPubkeys.includes(key)||protectedGrant.editorPubkeys.includes(key)||protectedGrant.creatorPubkey!==ADMIN)throw Error('Relay grants do not match the expected assigned/protected city setup.');
 const city={...saved.city,description:`Unapproved staging-only second-editor edit test ${crypto.randomUUID()}. Not a real walk.`};
 log('Approve editing the assigned test city.');
 const event=await publish(draft(city,Math.max(now(),(saved.editorLastTime||0)+1)),key);saved.editorLastTime=event.created_at;save(saved);await read({ids:[event.id],limit:1});
 log('PASS: second editor can edit assigned city; signed revision read back publicly.');
 log('Approve signing the protected-city edit attempt. It must be rejected.');
 await expectCityDenial(draft({...saved.protectedCity,description:`Unauthorized cross-city test ${crypto.randomUUID()}.`}),key);
 saved.isolationPassed=true;save(saved);log('PASS: relay rejected the second editor’s attempt to edit another registered city.');log('Switch to the super-admin and run step 7.');
}));
document.querySelector('#revoke-editor').addEventListener('click',()=>run(async()=>{
 const saved=load();const editor=collaborator(saved);const key=await identity();if(key!==ADMIN)throw Error('Select the super-admin for step 7.');
 if(!saved.isolationPassed)throw Error('Complete the access/isolation test in step 6 first.');
 const template=editorGrantUpdate(await currentGrant(saved.city.cityId),saved.city.cityId,saved.creator,editor,false,now(),NostrTools.verifyEvent);
 log(`Approve removing only ${editor} from test city ${saved.city.cityId}.`);
 const event=await publish(template,key);await read({ids:[event.id],limit:1});saved.revocationID=event.id;save(saved);
 log('PASS: updated editor list stored; creator and other editors preserved.');log('Switch to the second editor and run step 8.');
}));
document.querySelector('#test-revoked').addEventListener('click',()=>run(async()=>{
 const saved=load();const key=await identity();if(key!==collaborator(saved))throw Error('Select the second editor identity for step 8.');
 if(!saved.revocationID)throw Error('Complete admin revocation in step 7 first.');
 const grant=JSON.parse((await currentGrant(saved.city.cityId)).content);
 if(grant.editorPubkeys.includes(key)||grant.creatorPubkey===key)throw Error('Relay still lists this identity as an editor; revocation is not established.');
 log('Approve signing a fresh edit after revocation. It must be rejected.');
 await expectCityDenial(draft({...saved.city,description:`Revoked editor test ${crypto.randomUUID()}.`},Math.max(now(),(saved.editorLastTime||0)+1)),key);
 saved.revocationPassed=true;save(saved);log('PASS: relay rejected a fresh edit by the revoked editor.');log('Second-editor grant, city isolation and revocation verified. Share the output from steps 6 and 8. Both test cities remain unapproved.');
}));
