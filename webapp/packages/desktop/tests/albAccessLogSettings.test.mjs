import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import ts from 'typescript';
import { computed, reactive, ref } from 'vue';
const source = readFileSync(new URL('../src/components/ALBAccessLogSettings.vue', import.meta.url), 'utf8').match(/<script setup lang="ts">([\s\S]*?)<\/script>/)[1].replace(/^import .*$/gm, '');
const script = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.None } }).outputText;
class ApiError extends Error { constructor(status, code) { super(code); this.status = status; this.code = code; } }
const pending = () => { let resolve, reject; const promise = new Promise((a,b) => {resolve=a;reject=b}); return {promise,resolve,reject}; };
const config = () => ({ endpoint:'cn-hangzhou.log.aliyuncs.com',project:'test-project',logstore:'alb-access-log',alb_id:'alb-123',access_key_id:'syntheticID',secret_set:true,version:1 });
const result = (status='success') => ({ status, tested_at:'2026-10-09T00:00:00Z',from:1,to:901,request_count:status==='success'?10:0 });
function panel() {
 const requests=[], notices=[], cleanup=[];
 const build = new Function('computed','reactive','ref','onMounted','onUnmounted','ApiError','client','ElMessage', script+';return {form,secret,ready,busy,error,result,currentResult,statusLabel,load,submit};');
 const state=build(computed,reactive,ref,()=>{},fn=>cleanup.push(fn),ApiError,{ request:(path,options)=>{const p=pending(); requests.push({...p,path,options}); return p.promise;} },{success:m=>notices.push(m),warning:m=>notices.push(m)});
 return {...state,requests,notices,stop:()=>cleanup.forEach(fn=>fn())};
}
async function loaded(p) { const run=p.load();p.requests[0].resolve({configured:true,connection:{...config(),last_test:result()}});await run; }
test('draft edit hides old success; test uses ALB endpoint and only whitelisted fields',async()=>{
 const p=panel();await loaded(p);assert.equal(p.statusLabel.value,'连接成功');
 p.form.alb_id='alb-456';assert.equal(p.currentResult.value,undefined);
 const run=p.submit(false),req=p.requests.at(-1),body=JSON.parse(req.options.body);
 assert.equal(req.path,'/api/dashboard/alb-access-log/test');assert.equal(body.access_key_secret,'');assert.equal(body.last_test,undefined);assert.equal(body.secret_set,undefined);
 req.resolve({connection:{...config(),alb_id:'alb-456',last_test:result('no_data')},saved:false,persisted:false});await run;
 assert.equal(p.statusLabel.value,'连接成功，暂无日志');p.stop();
});
test('saving clears plaintext secret; failure status is not shown as connected',async()=>{
 const p=panel();await loaded(p);p.secret.value='synthetic-new-secret';
 const run=p.submit(true),req=p.requests.at(-1);
 assert.equal(req.options.method,'PUT');assert.equal(JSON.parse(req.options.body).access_key_secret,'synthetic-new-secret');
 req.resolve({connection:{...config(),version:2,last_test:{...result('failed'),code:'alb_auth_failed'}},saved:true,persisted:true});await run;
 assert.equal(p.secret.value,'');assert.equal(p.statusLabel.value,'连接失败');assert.match(p.notices[0],/尚未通过/);p.stop();
});
test('late load and unmounted response cannot replace newer state or resurrect a secret',async()=>{
 const p=panel();const first=p.load(),second=p.load();
 p.requests[1].resolve({configured:true,connection:{...config(),version:4}});await second;
 p.requests[0].resolve({configured:true,connection:{...config(),version:1}});await first;assert.equal(p.form.version,4);
 p.secret.value='draft';const run=p.submit(false);p.stop();
 p.requests.at(-1).resolve({connection:{...config(),last_test:result()},saved:false,persisted:false});await run;
 assert.equal(p.secret.value,'');assert.equal(p.form.version,4);
});
test('load failure disables actions and a conflict is explicit',async()=>{
 const p=panel();const load=p.load();p.requests[0].reject(new Error('offline'));await load;
 assert.equal(p.ready.value,false);await p.submit(true);assert.equal(p.requests.length,1);
 const reload=p.load();p.requests[1].resolve({configured:true,connection:config()});await reload;
 const save=p.submit(true);p.requests[2].reject(new ApiError(409,'alb_config_conflict'));await save;
 assert.match(p.error.value,/其他操作更新/);assert.equal(p.currentResult.value,undefined);p.stop();
});
