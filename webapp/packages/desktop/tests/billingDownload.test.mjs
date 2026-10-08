import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import ts from 'typescript';
import {ref} from 'vue';
const compile=f=>ts.transpile(readFileSync(new URL('../src/utils/'+f,import.meta.url),'utf8').replace(/^import .*;\r?\n/gm,''),{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.CommonJS});
test('download waits for server receipt, reports failure, and never fetches a Blob',async()=>{
 for(const state of ['ready','error','timeout']){
  let tick,frame,cleared=false;const document={cookie:'',createElement:()=>({style:{},remove(){}}),body:{appendChild(v){frame=v;}}};
  const window={setInterval(fn){tick=fn;return 1},clearInterval(){cleared=true},setTimeout(){}};
  const exports={};new Function('exports','document','window','crypto',compile('httpError.ts'))(exports,document,window,{getRandomValues:v=>v.fill(10)});
  const result=exports.prepareBillingFileDownload('/api/dashboard/billing/statements/result?id=x');
  let settled=false;const checked=result.then(()=>{settled=true;return 'ready'},e=>{settled=true;return e.message});await Promise.resolve();assert.equal(settled,false);
  assert.ok(frame.src.includes('download_token='));assert.equal(frame.style.display,'none');
  if(state!=='timeout'){document.cookie='ct_download_'+('0a'.repeat(16))+'='+state;tick();}else{for(let i=0;i<1200;i++)tick();}
  const message=await checked;assert.equal(cleared,true);if(state==='ready')assert.equal(message,'ready');else assert.match(message,state==='error'?/失败/:/下载列表/);
 }
});
test('all mounted billing views share duplicate suppression and release after completion',async()=>{
 let finish,calls=0,closed=0;const messages=[];
 const ElMessage=Object.assign(v=>({close(){closed++}}),{success:v=>messages.push(v),error:v=>messages.push(v)});
 const exports={};new Function('exports','ref','ElMessage','prepareBillingFileDownload',compile('billingDownload.ts'))(exports,ref,ElMessage,()=>{calls++;return new Promise(r=>finish=r)});
 const a=exports.useBillingDownload(),b=exports.useBillingDownload();const work=a.download('bill','/file');await b.download('bill','/file');assert.equal(calls,1);assert.deepEqual(a.pending.value,['bill']);assert.deepEqual(b.pending.value,['bill']);finish();await work;assert.deepEqual(a.pending.value,[]);assert.equal(closed,1);assert.match(messages[0],/浏览器下载列表/);
});
