import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import ts from 'typescript';
import {ref,reactive,computed,watch,effectScope} from 'vue';
const source=readFileSync(new URL('../src/components/ChannelDiscountEditor.vue',import.meta.url),'utf8');
const script=ts.transpile(source.split('<script setup lang="ts">')[1].split('</script>')[0].replace(/^import .*;\r?\n/gm,''),{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.None});
const wait=()=>new Promise(r=>setImmediate(r));
const deferred=()=>{let resolve;const promise=new Promise(r=>resolve=r);return {promise,resolve};};
async function page(t){
 const props=reactive({site:'a',upstream:1,channel:10,name:'channel'}),calls=[],events=[],messages=[];
 let rules=[{id:7,subject_id:1,channel_id:10,discount:'0.8',effective_from:'2026-07-01T00:00:00Z',effective_to:'2026-08-01T00:00:00Z'},{id:8,subject_id:1,channel_id:10,discount:'1',effective_from:'2026-08-05T00:00:00Z'}];
 const dashboard={billingDiscounts:async()=>({items:[...rules]}),deleteBillingDiscount:async(site,id)=>{calls.push([site,id]);rules=rules.filter(r=>r.id!==id);},saveBillingDiscount:async()=>{}};
 const confirm={confirm:async()=>{}};
 const scope=effectScope();t.after(()=>scope.stop());
 const factory=new Function('computed','ref','reactive','watch','onBeforeUnmount','defineProps','defineEmits','ElMessage','ElMessageBox','ApiError','dashboard','formatBillingDiscount',script+';return {open,saving,deleting,loading,loaded,error,items,form,edit,load,save,remove};');
 const p=scope.run(()=>factory(computed,ref,reactive,watch,()=>{},()=>props,()=>name=>events.push(name),{success:m=>messages.push(m)},confirm,class extends Error{},dashboard,String));
 p.open.value=true;await wait();return {...p,props,calls,events,messages,confirm,dashboard};
}
test('delete existing full-price rule refreshes list and resets deleted editor',async t=>{
 const p=await page(t);const rule=p.items.value.find(r=>r.id===8);p.edit(rule);await p.remove(rule);
 assert.deepEqual(p.calls,[['a',8]]);assert.deepEqual(p.items.value.map(r=>r.id),[7]);assert.equal(p.form.id,0);assert.deepEqual(p.events,['changed']);assert.equal(p.deleting.value,0);
});
test('cancel deletion sends no request and preserves draft',async t=>{
 const p=await page(t);p.edit(p.items.value[0]);p.confirm.confirm=async()=>{throw Error('cancel');};await p.remove(p.items.value[0]);assert.equal(p.calls.length,0);assert.equal(p.form.id,8);assert.equal(p.items.value.length,2);assert.equal(p.error.value,'');
});
test('delete failure preserves rule and exposes retry message',async t=>{
 const p=await page(t);p.dashboard.deleteBillingDiscount=async()=>{throw Error('offline');};await p.remove(p.items.value[0]);assert.equal(p.items.value.length,2);assert.match(p.error.value,/删除失败/);assert.equal(p.events.length,0);assert.equal(p.deleting.value,0);
});
test('pending confirmation cannot delete a different channel after context change',async t=>{
 const p=await page(t),pending=deferred();p.confirm.confirm=()=>pending.promise;const action=p.remove(p.items.value[0]);p.props.channel=20;pending.resolve();await action;assert.equal(p.calls.length,0);assert.equal(p.open.value,false);
});
test('delete is mutually exclusive and late completion cannot alter reopened editor',async t=>{
 const p=await page(t),pending=deferred();p.dashboard.deleteBillingDiscount=async(site,id)=>{p.calls.push([site,id]);return pending.promise;};const action=p.remove(p.items.value[0]);await wait();await p.remove(p.items.value[0]);await p.save();assert.equal(p.calls.length,1);
 p.props.site='b';p.open.value=true;await wait();p.form.discount=0.7;pending.resolve();await action;assert.equal(p.form.discount,0.7);assert.equal(p.messages.length,0);assert.equal(p.events.length,0);
});
test('old list response is ignored after channel switch',async t=>{
 const p=await page(t),pending=deferred();p.dashboard.billingDiscounts=()=>pending.promise;const loading=p.load();p.props.channel=20;pending.resolve({items:[{id:99,subject_id:1,channel_id:10}]});await loading;assert.deepEqual(p.items.value,[]);assert.equal(p.loaded.value,false);
});
