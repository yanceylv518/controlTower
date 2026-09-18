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
const deferred = () => { let resolve; const promise = new Promise(r => resolve = r); return { promise, resolve }; };
const fixture = () => ({ items: [{id:1,name:'One',remark:'',enabled:true,instance_id:'a',channels:[{channel_id:10,channel_name:'ten'},{channel_id:99,channel_name:'retired'}]},{id:2,name:'Two',remark:'',enabled:false,instance_id:'a',channels:[{channel_id:20,channel_name:'twenty'}]}], channels:[{channel_id:10,channel_name:'ten',status:1,models:'glm'},{channel_id:20,channel_name:'twenty',status:2,models:'gpt'},{channel_id:30,channel_name:'thirty',status:1,models:'glm'},{channel_id:30,channel_name:'thirty',status:1,models:'glm'}] });
async function page(t) {
  const filters=reactive({site_id:'a',loadInstances:async()=>{}}), calls=[], messages=[];
  const dashboard={billingUpstreams:async()=>fixture(),saveBillingUpstream:async p=>{calls.push(p);return {...p,id:p.id||3}},deleteBillingUpstream:async(...args)=>calls.push(args)};
  const confirm = { confirm:async()=>{} };
  const scope=effectScope();t.after(()=>scope.stop());
  const names=['computed','onBeforeUnmount','reactive','ref','watch','ElMessage','ElMessageBox','dashboard','useAsyncData','useFiltersStore'];
  const create=new Function(...names,`${script};return {state,current,items,allItems,channels,unassigned,enabledCount,selected,active,search,statusFilter,drawerOpen,form,ready,openEditor,toggleChannel,pickerChannels,selectedOnly,save,remove};`);
  const p=scope.run(()=>create(computed,()=>{},reactive,ref,watch,{warning:m=>messages.push(m),error:m=>messages.push(m),success:m=>messages.push(m)},confirm,dashboard,useAsyncData,()=>filters));
  await p.state.reload();await nextTick();return {...p,filters,dashboard,calls,messages,confirm};
}
test('current-site summary deduplicates channels, excludes missing channels and stays stable under search',async t=>{
 const p=await page(t);assert.equal(p.channels.value.length,3);assert.equal(p.unassigned.value.length,1);assert.equal(p.enabledCount.value,1);p.search.value='Two';await nextTick();assert.equal(p.items.value.length,1);assert.equal(p.selected.value,2);assert.equal(p.channels.value.length-p.unassigned.value.length,2);
});
test('editor copies associations, preserves unavailable channels and blocks occupied channels',async t=>{
 const p=await page(t);p.openEditor(p.allItems.value[0]);p.form.name='changed';assert.equal(p.allItems.value[0].name,'One');assert.ok(p.pickerChannels.value.some(c=>c.channel_id===99));p.toggleChannel(p.channels.value[1],true);assert.equal(p.form.channels.length,2);p.toggleChannel(p.channels.value[2],true);assert.equal(p.form.channels.length,3);p.toggleChannel(p.channels.value[0],false);assert.equal(p.allItems.value[0].channels.length,2);
});
test('site switch closes editor, clears stale data and rejects late old-site response',async t=>{
 const p=await page(t);p.openEditor(p.active.value);const old=deferred(),fresh=deferred();p.dashboard.billingUpstreams=site=>site==='a'?old.promise:fresh.promise;const first=p.state.reload();p.filters.site_id='b';assert.equal(p.drawerOpen.value,false);assert.equal(p.current.value,undefined);await p.save();assert.equal(p.calls.length,0);old.resolve(fixture());await first;assert.equal(p.current.value,undefined);fresh.resolve({items:[],channels:[]});await new Promise(r=>setImmediate(r));assert.equal(p.current.value.site,'b');assert.equal(p.allItems.value.length,0);
});
test('save snapshots original site, prevents duplicates and does not close new-site editor state',async t=>{
 const p=await page(t);p.openEditor(p.active.value);const pending=deferred();p.dashboard.saveBillingUpstream=payload=>{p.calls.push(payload);return pending.promise};const saving=p.save();await p.save();assert.equal(p.calls.length,1);p.filters.site_id='b';pending.resolve({id:1});await saving;assert.equal(p.calls[0].instance_id,'a');assert.equal(p.messages.length,0);
});
test('delete cancelled or site changed during confirmation sends no mutation',async t=>{
 const p=await page(t);p.confirm.confirm=async()=>{throw 'cancel'};await p.remove();assert.equal(p.calls.length,0);const pending=deferred();p.confirm.confirm=()=>pending.promise;const deleting=p.remove();p.filters.site_id='b';pending.resolve();await deleting;assert.equal(p.calls.length,0);
});

test('most channels sort first, ties retain source order, filtering does not reorder source',async t=>{
 const p=await page(t);const extra={...p.allItems.value[0],id:3,name:'Three',channels:[{channel_id:40},{channel_id:41},{channel_id:42}]};p.state.data.value={...p.state.data.value,items:[p.allItems.value[1],extra,p.allItems.value[0],{...extra,id:4,name:'Four'}]};await nextTick();assert.deepEqual(p.items.value.map(u=>u.id),[3,4,1,2]);assert.deepEqual(p.allItems.value.map(u=>u.id),[2,3,1,4]);p.statusFilter.value='enabled';assert.deepEqual(p.items.value.map(u=>u.id),[3,4,1]);
});
