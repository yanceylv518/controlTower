import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import ts from 'typescript';
import { computed, ref, shallowRef, reactive, watch, effectScope, nextTick } from 'vue';
const source = readFileSync(new URL('../src/views/BillingUpstreamsView.vue', import.meta.url), 'utf8');
const compile = value => ts.transpile(value, { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.None });
const script = compile(source.split('<script setup lang="ts">')[1].split('</script>')[0].replace(/^import .*;\r?\n/gm, ''));
const asyncSource = readFileSync(new URL('../src/composables/useAsyncData.ts', import.meta.url), 'utf8').replace(/^import .*\r?\n/gm, '').replace('export function', 'function');
const useAsyncData = new Function('ref', 'shallowRef', 'billingReadErrorMessage', `${compile(asyncSource)};return useAsyncData`)(ref, shallowRef, e => e.message);
const normalizeSource = readFileSync(new URL('../src/utils/upstreamUrl.ts', import.meta.url), 'utf8').replace('export function', 'function');
const normalizeUpstreamUrl = new Function(`${compile(normalizeSource)};return normalizeUpstreamUrl`)();
const deferred = () => { let resolve; const promise = new Promise(r => resolve = r); return { promise, resolve }; };
const settle = () => new Promise(r => setImmediate(r));
const fixture = () => ({ source_available: true, items: [
  { id:1, revision:3, name:'One', remark:'', enabled:true, instance_id:'a', channel_prefixes:['one'], suggested_prefixes:['legacy'], review_prefixes:['one'], urls:['https://one.example','https://shared.example','https://old.example'], channels:[{channel_id:10,channel_name:'one_glm',association_source:'auto',matched_prefix:'one'},{channel_id:99,channel_name:'retired',selected_models:['old-model']}] },
  { id:2, revision:4, name:'Two', remark:'', enabled:false, instance_id:'a', channel_prefixes:['two','one_plus'], urls:['https://shared.example'], channels:[{channel_id:20,channel_name:'two_gpt',association_source:'manual'}] },
], channels:[
  {channel_id:10,channel_name:'one_glm',status:1,models:'glm',base_url:'https://one.example'},
  {channel_id:20,channel_name:'two_gpt',status:2,models:'gpt',base_url:'https://shared.example'},
  {channel_id:30,channel_name:'one_new',status:1,models:'glm',base_url:'https://new.example'},
  {channel_id:30,channel_name:'one_new',status:1,models:'glm',base_url:'https://new.example'},
] });
async function page(t, overrides = {}) {
  const filters=reactive({site_id:'a',loadInstances:async()=>{}}), calls=[], messages=[], events=[], confirmations=[];
  const dashboard={
    billingDiscounts:async()=>({items:[]}),
    billingUpstreams:async(site,signal)=>{events.push({kind:'read',site,signal});return fixture();},
    syncBillingUpstreams:async(site,ids)=>{events.push({kind:'sync',site,ids});return fixture();},
    saveBillingUpstream:async p=>{calls.push(p);return {...p,id:p.id||3};},
    ...overrides,
  };
  const confirm = { confirm:async(message,title)=>{confirmations.push({message,title});} };
  const scope=effectScope(),unmount=[];t.after(()=>{unmount.forEach(fn=>fn());scope.stop();});
  const names=['computed','onBeforeUnmount','reactive','ref','watch','ElMessage','ElMessageBox','dashboard','useAsyncData','useFiltersStore','normalizeUpstreamUrl'];
  const create=new Function(...names,`${script};return {state,current,discountRules,currentDiscount,items,allItems,channels,unassigned,enabledCount,selected,active,search,statusFilter,drawerOpen,form,configuredChannelIDs,originalChannelIDs,configurableChannels,occupiedByOther,ready,sourceAvailable,openEditor,formPrefixes,prefixSuggestions,prefixTransfers,prefixPreview,channelTransfers,urlRecords,associationLabel,configurationLabel,save,saveError,revisionConflict,reloadEditor,synchronize,syncFeedback,syncing,addSuggestedPrefix,deleteOpen,deleteTarget,deleting,openDelete,removeUpstream};`);
  const p=scope.run(()=>create(computed,fn=>unmount.push(fn),reactive,ref,watch,{warning:m=>messages.push(m),error:m=>messages.push(m),success:m=>messages.push(m)},confirm,dashboard,useAsyncData,()=>filters,normalizeUpstreamUrl));
  await settle();await nextTick();return {...p,filters,dashboard,calls,messages,events,confirmations,confirm};
}

test('initialization explicitly synchronizes before reading; ordinary refresh is read-only',async t=>{
  const p=await page(t);assert.deepEqual(p.events.map(e=>e.kind),['sync','read']);
  await p.state.reload();assert.deepEqual(p.events.map(e=>e.kind),['sync','read','read']);
  assert.ok(p.events.at(-1).signal instanceof AbortSignal);
});

test('current-site summary deduplicates channels and stays stable under search',async t=>{
  const p=await page(t);assert.equal(p.channels.value.length,3);assert.equal(p.unassigned.value.length,1);assert.equal(p.enabledCount.value,1);
  p.search.value='Two';await nextTick();assert.equal(p.items.value.length,1);assert.equal(p.selected.value,2);
  p.search.value='one_plus';await nextTick();assert.equal(p.items.value[0].id,2);
});

test('shared URL can be recorded without claiming channels and existing model choices remain intact',async t=>{
  const p=await page(t);p.openEditor(p.allItems.value[0]);p.form.name='changed';p.form.url=' HTTPS://SHARED.example:443/ ';
  assert.equal(p.allItems.value[0].name,'One');assert.deepEqual(p.form.channels[1].selected_models,['old-model']);
  await p.save();assert.equal(p.calls.length,1);assert.equal(p.calls[0].url,'HTTPS://SHARED.example:443/');
  assert.deepEqual(p.calls[0].channels,[]);assert.deepEqual(p.calls[0].add_channel_ids,[]);assert.equal(p.calls[0].urls,undefined);assert.equal(p.calls[0].revision,3);
});

test('late old-site response cannot overwrite current site or current discount',async t=>{
  const p=await page(t),old=deferred();
  const b={...fixture(),items:[{...fixture().items[0],id:101,instance_id:'b'}]};
  p.dashboard.billingUpstreams=async site=>site==='a'?fixture():b;
  p.dashboard.billingDiscounts=site=>site==='a'?old.promise:Promise.resolve({items:[{subject_id:101,channel_id:10,discount:'0.7',effective_from:'2020-01-01T00:00:00Z'}]});
  p.openEditor(p.active.value);const first=p.state.reload();p.filters.site_id='b';
  assert.equal(p.drawerOpen.value,false);assert.equal(p.current.value,undefined);await p.save();assert.equal(p.calls.length,0);
  await settle();assert.equal(p.current.value.site,'b');assert.equal(p.currentDiscount(10)?.discount,'0.7');
  old.resolve({items:[{subject_id:1,channel_id:10,discount:'0.8',effective_from:'2020-01-01T00:00:00Z'}]});await first;
  assert.equal(p.current.value.site,'b');assert.equal(p.currentDiscount(10)?.discount,'0.7');
});

test('late previous refresh cannot replace newer discounts within one site',async t=>{
  const p=await page(t),old=deferred();p.dashboard.billingDiscounts=()=>old.promise;const first=p.state.reload();
  p.dashboard.billingDiscounts=async()=>({items:[{subject_id:1,channel_id:10,discount:'0.6',effective_from:'2020-01-01T00:00:00Z'}]});await p.state.reload();
  old.resolve({items:[]});await first;assert.equal(p.currentDiscount(10)?.discount,'0.6');
});

test('discount read failure does not block upstream metadata editing',async t=>{
  const p=await page(t,{billingDiscounts:async()=>{throw Error('offline');}});
  assert.equal(p.ready.value,true);assert.match(p.current.value.discountError,/折扣/);p.openEditor(p.active.value);p.form.remark='updated';await p.save();assert.equal(p.calls.length,1);
});

test('offline source preserves saved associations, marks URL use unknown and permits basic edits',async t=>{
  const offline={...fixture(),source_available:false,channels:[],sync_error:'readonly_source_unavailable'};
  const p=await page(t,{billingUpstreams:async()=>offline,syncBillingUpstreams:async()=>{throw Error('offline');}});
  assert.equal(p.ready.value,true);assert.equal(p.sourceAvailable.value,false);assert.equal(p.allItems.value.length,2);
  p.openEditor(p.allItems.value[0]);assert.deepEqual(p.configuredChannelIDs.value,[10,99]);assert.ok(p.urlRecords.value.every(r=>r.label==='使用状态未知'));
  p.form.name='Offline renamed';await p.save();assert.equal(p.calls[0].name,'Offline renamed');assert.deepEqual(p.calls[0].add_channel_ids,[]);
});

test('offline source cannot submit channel additions or removals',async t=>{
  const p=await page(t,{billingUpstreams:async()=>({...fixture(),source_available:false,channels:[]})});
  p.openEditor(p.active.value);p.configuredChannelIDs.value.push(123);await p.save();assert.equal(p.calls.length,0);assert.match(p.messages.at(-1),/渠道源不可用/);
});

test('URL list distinguishes current use, historical records and shared records',async t=>{
  const p=await page(t);p.openEditor(p.allItems.value[0]);
  assert.equal(p.urlRecords.value[0].count,1);assert.match(p.urlRecords.value[0].label,/当前使用/);
  assert.equal(p.urlRecords.value[1].shared,1);assert.equal(p.urlRecords.value[2].label,'历史 / 手工记录');
});

test('prefix preview follows exact or underscore-boundary match, case and longest prefix ownership',async t=>{
  const p=await page(t);p.state.data.value.channels.push(
    {channel_id:40,channel_name:'one',status:1,models:''},
    {channel_id:41,channel_name:'one_plus_model',status:1,models:''},
    {channel_id:42,channel_name:'One_model',status:1,models:''},
    {channel_id:43,channel_name:'oneElse',status:1,models:''},
    {channel_id:44,channel_name:'one_excluded',status:1,models:'',auto_excluded:true});
  p.openEditor(p.allItems.value[0]);assert.deepEqual(p.prefixPreview.value.automatic.map(c=>c.channel_id),[30,40]);assert.deepEqual(p.prefixPreview.value.excluded.map(c=>c.channel_id),[44]);
  p.addSuggestedPrefix('one_plus');assert.deepEqual(p.prefixTransfers.value.map(v=>v.prefix),['one_plus']);assert.deepEqual(p.prefixPreview.value.automatic.map(c=>c.channel_id),[30,40,41]);
});

test('suggested prefixes require explicit addition and are independent of display name',async t=>{
  const p=await page(t);p.openEditor(p.allItems.value[0]);assert.deepEqual(p.formPrefixes.value,['one']);assert.deepEqual(p.prefixSuggestions.value,['legacy']);
  p.form.name='Different display';p.addSuggestedPrefix('legacy');p.form.channel_prefixes.push('third_');await p.save();
  assert.deepEqual(p.calls[0].channel_prefixes,['one','legacy','third']);assert.deepEqual(p.allItems.value[0].channel_prefixes,['one']);
});

test('manual channel selection does not need a matching prefix and sends only deltas',async t=>{
  const p=await page(t);p.state.data.value.channels.find(c=>c.channel_id===30).channel_name='unrelated';p.openEditor(p.allItems.value[0]);
  p.configuredChannelIDs.value=[99,30];await p.save();assert.deepEqual(p.calls[0].add_channel_ids,[30]);assert.deepEqual(p.calls[0].remove_channel_ids,[10]);assert.deepEqual(p.calls[0].channel_transfers,[]);
});

test('channel transfer requires concrete confirmation and uses atomic ownership compare fields',async t=>{
  const p=await page(t);p.openEditor(p.allItems.value[0]);p.configuredChannelIDs.value.push(20);await p.save();
  assert.equal(p.confirmations.length,1);assert.match(p.confirmations[0].message,/two_gpt.*#20：Two → One/);assert.match(p.confirmations[0].message,/折扣不迁移/);assert.match(p.confirmations[0].message,/覆盖生成/);
  assert.deepEqual(p.calls[0].channel_transfers,[{channel_id:20,from_upstream_id:2}]);assert.deepEqual(p.calls[0].add_channel_ids,[]);
});

test('prefix transfer changes rule ownership but never implicitly transfers its existing channels',async t=>{
  const p=await page(t);p.openEditor(p.allItems.value[0]);p.form.channel_prefixes.push('two');await p.save();
  assert.deepEqual(p.calls[0].prefix_transfers,[{prefix:'two',from_upstream_id:2}]);assert.deepEqual(p.calls[0].channel_transfers,[]);assert.deepEqual(p.calls[0].add_channel_ids,[]);
  assert.match(p.confirmations[0].message,/前缀「two」：Two → One/);
});

test('cancelled transfer keeps draft and makes no mutation',async t=>{
  const p=await page(t);p.openEditor(p.active.value);p.configuredChannelIDs.value.push(20);p.confirm.confirm=async()=>{throw Error('cancel');};await p.save();
  assert.equal(p.calls.length,0);assert.equal(p.drawerOpen.value,true);assert.ok(p.configuredChannelIDs.value.includes(20));
});

test('site round trip while transfer confirmation is open invalidates the old action',async t=>{
  const p=await page(t),pending=deferred();p.openEditor(p.active.value);p.configuredChannelIDs.value.push(20);p.confirm.confirm=()=>pending.promise;const saving=p.save();
  p.filters.site_id='b';p.filters.site_id='a';await settle();p.openEditor();p.form.name='New draft';pending.resolve();await saving;
  assert.equal(p.calls.length,0);assert.equal(p.drawerOpen.value,true);assert.equal(p.form.name,'New draft');
});

test('save is mutually exclusive and old completion cannot close a new-site draft',async t=>{
  const p=await page(t),pending=deferred();p.openEditor(p.active.value);p.dashboard.saveBillingUpstream=payload=>{p.calls.push(payload);return pending.promise;};
  const saving=p.save();await p.save();assert.equal(p.calls.length,1);p.filters.site_id='b';await settle();p.openEditor();p.form.name='B draft';pending.resolve({id:1});await saving;
  assert.equal(p.calls[0].instance_id,'a');assert.equal(p.drawerOpen.value,true);assert.equal(p.form.name,'B draft');assert.equal(p.messages.length,0);
});

test('revision conflict preserves edits and offers reload rather than blindly overwriting',async t=>{
  const p=await page(t);p.openEditor(p.active.value);p.form.name='Draft';p.dashboard.saveBillingUpstream=async()=>{throw Error('upstream_revision_conflict');};await p.save();
  assert.equal(p.drawerOpen.value,true);assert.equal(p.form.name,'Draft');assert.equal(p.revisionConflict.value,true);assert.match(p.saveError.value,/其他操作更新/);
  await p.reloadEditor();assert.equal(p.form.name,'One');assert.equal(p.form.revision,3);
});

test('restoring automatic matching is an explicit sync operation, not a metadata save',async t=>{
  const p=await page(t);await p.synchronize([30]);assert.deepEqual(p.events.filter(e=>e.kind==='sync').at(-1).ids,[30]);assert.equal(p.calls.length,0);assert.match(p.messages.at(-1),/恢复自动匹配/);
});

test('saving with a sync failure reports saved configuration separately from channel synchronization',async t=>{
  const p=await page(t);p.openEditor(p.active.value);p.dashboard.saveBillingUpstream=async payload=>({...payload,sync_error:'timeout'});await p.save();
  assert.equal(p.drawerOpen.value,false);assert.match(p.syncFeedback.value,/已保存/);assert.match(p.syncFeedback.value,/同步未完成/);
});

test('association origin labels distinguish automatic, manual and legacy data',async t=>{
  const p=await page(t);assert.equal(p.associationLabel({association_source:'auto',matched_prefix:'one'}),'前缀匹配');assert.equal(p.associationLabel({association_source:'manual'}),'手动配置');assert.equal(p.associationLabel({}),'历史关联');p.selected.value='unassigned';assert.equal(p.associationLabel({auto_excluded:true}),'已暂停自动匹配');
});

test('new upstream supports prefixes without URL and validates optional URLs',async t=>{
  const p=await page(t);p.openEditor();p.form.name='Future';p.form.channel_prefixes=['future'];await p.save();assert.equal(p.calls.length,1);
  p.openEditor();p.form.name='Invalid URL';p.form.url='not a URL';await p.save();assert.equal(p.calls.length,1);
});

test('URL normalization preserves endpoint boundaries',()=>{
  assert.equal(normalizeUpstreamUrl(' HTTPS://API.example.com:443/v1/// '),'https://api.example.com/v1');
  assert.notEqual(normalizeUpstreamUrl('https://api.example.com/v1'),normalizeUpstreamUrl('https://api.example.com/v10'));
  assert.notEqual(normalizeUpstreamUrl('https://api.example.com'),normalizeUpstreamUrl('http://api.example.com'));
  for(const value of ['example.com','https://user:pass@example.com','https://example.com?key=x','https://example.com#x','https://example.com:99999'])assert.equal(normalizeUpstreamUrl(value),'');
});


test('late editor reload does not reopen a closed dialog or replace a different draft',async t=>{
  const p=await page(t),pending=deferred();p.openEditor(p.active.value);p.dashboard.billingUpstreams=()=>pending.promise;
  const reload=p.reloadEditor();p.drawerOpen.value=false;pending.resolve(fixture());await reload;assert.equal(p.drawerOpen.value,false);
});

test('missing historical channels are not automatic candidates or transferable additions',async t=>{
  const p=await page(t);p.state.data.value.channels.push({channel_id:88,channel_name:'one_gone',source_missing:true,status:0,models:''});p.openEditor(p.active.value);
  assert.ok(!p.prefixPreview.value.automatic.some(c=>c.channel_id===88));assert.ok(!p.unassigned.value.some(c=>c.channel_id===88));
  p.configuredChannelIDs.value.push(88);await p.save();assert.equal(p.calls.length,0);assert.match(p.messages.at(-1),/不在当前目录/);
});

test('transfer ownership conflict has actionable localized recovery',async t=>{
  const p=await page(t);p.openEditor(p.active.value);p.configuredChannelIDs.value.push(20);p.dashboard.saveBillingUpstream=async()=>{throw Error('upstream_transfer_conflict');};await p.save();
  assert.equal(p.revisionConflict.value,true);assert.match(p.saveError.value,/归属已变化/);assert.equal(p.drawerOpen.value,true);
});


test('retaining historical inferred prefixes explicitly confirms activation even for metadata-only changes',async t=>{
  const p=await page(t);p.openEditor(p.active.value);p.form.remark='Only changing remark';await p.save();
  assert.equal(p.confirmations.length,1);assert.match(p.confirmations[0].message,/确认启用历史推导前缀「one」/);assert.equal(p.confirmations[0].title,'确认前缀规则');
});

test('removing unconfirmed historical prefixes does not activate or request confirmation for them',async t=>{
  const p=await page(t);p.openEditor(p.active.value);p.form.channel_prefixes=[];await p.save();
  assert.equal(p.confirmations.length,0);assert.deepEqual(p.calls[0].channel_prefixes,[]);
});


test('successful save with failed audit never suggests repeating transfer or synchronization',async t=>{
  const p=await page(t);p.openEditor(p.active.value);p.configuredChannelIDs.value.push(20);
  p.dashboard.saveBillingUpstream=async payload=>({...payload,sync_error:'billing_upstream_audit_failed'});await p.save();
  assert.equal(p.drawerOpen.value,false);assert.equal(p.syncFeedback.value,'配置已保存，操作记录写入失败。');assert.equal(p.messages.at(-1),'配置已保存');
  assert.doesNotMatch(p.syncFeedback.value,/未完成|重试|稍后|不可用|待同步/);assert.equal(p.sourceAvailable.value,true);
});

test('successful synchronization with failed audit stays online and does not recommend repeating it',async t=>{
  const p=await page(t);p.dashboard.syncBillingUpstreams=async()=>({...fixture(),sync_error:'billing_upstream_audit_failed'});await p.synchronize();
  assert.equal(p.syncFeedback.value,'渠道同步已完成，操作记录写入失败。');assert.equal(p.sourceAvailable.value,true);
  assert.doesNotMatch(p.syncFeedback.value,/未完成|重试|稍后|不可用/);
});

test('URL preview precedes conflicting prefixes and shared URL requires a matching owner',async t=>{
 const data=fixture();data.channels.push({channel_id:71,channel_name:'two_new',base_url:'https://one.example'},{channel_id:72,channel_name:'unknown_new',base_url:'https://shared.example'});
 const p=await page(t,{billingUpstreams:async()=>data});p.openEditor(p.allItems.value[0]);
 assert.ok(p.prefixPreview.value.automatic.some(c=>c.channel_id===71));assert.ok(!p.prefixPreview.value.automatic.some(c=>c.channel_id===72));
 assert.equal(p.associationLabel({association_source:'auto'}),'URL 匹配');
});
test('deleting occupied upstream requires merge target and sends both revisions',async t=>{
 const deleted=[];const p=await page(t,{deleteBillingUpstream:async(...args)=>{deleted.push(args);return {deleted:true,archived:true};}});
 p.openEditor(p.allItems.value[0]);p.openDelete();await p.removeUpstream();assert.equal(deleted.length,0);
 p.deleteTarget.value=2;await p.removeUpstream();assert.deepEqual(deleted,[['a',1,3,2,4]]);assert.equal(p.deleteOpen.value,false);assert.equal(p.drawerOpen.value,false);
});
test('deletion conflict preserves dialog and cancelled confirmation never deletes',async t=>{
 const p=await page(t,{deleteBillingUpstream:async()=>{throw Error('discount_overlap')}});p.openEditor(p.allItems.value[0]);p.openDelete();p.deleteTarget.value=2;
 await p.removeUpstream();assert.equal(p.deleteOpen.value,true);assert.ok(p.messages.some(m=>m.includes('折扣有效期重叠')));
 let called=false;p.dashboard.deleteBillingUpstream=async()=>{called=true};p.confirm.confirm=async()=>{throw Error('cancel')};await p.removeUpstream();assert.equal(called,false);
});
test('late delete response cannot close a new-site editor',async t=>{
 const wait=deferred();const p=await page(t,{deleteBillingUpstream:()=>wait.promise});p.openEditor(p.allItems.value[0]);p.openDelete();p.deleteTarget.value=2;
 const deleting=p.removeUpstream();await settle();p.filters.site_id='b';await settle();p.openEditor(p.allItems.value[0]);wait.resolve({deleted:true});await deleting;assert.equal(p.drawerOpen.value,true);
});
