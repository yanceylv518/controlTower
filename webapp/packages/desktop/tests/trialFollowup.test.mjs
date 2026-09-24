import test from 'node:test'
import assert from 'node:assert/strict'
import {readFileSync} from 'node:fs'
import ts from 'typescript'
import {computed,ref} from 'vue'
test('404 presents labelled sample data and blocks preview writes and identity requests',async()=>{
 const s=harness();s.client.request=async()=>{throw new Error('http_404')};await s.load()
 assert.equal(s.data.value.state,'preview');assert.equal(s.data.value.site_id,'a');assert.equal(s.data.value.watches[0].label,'示例客户')
 let requests=0;s.client.request=async()=>{requests++;throw new Error('unexpected request')}
 await s.loadUsers();s.draft.value.user_id=101;await s.loadKeys();await s.saveWatch(true);await s.saveWatch(false)
 assert.equal(s.users.value[0].name,'示例测试账户');assert.equal(s.keys.value[0].id,201);assert.equal(requests,0)
 s.client.request=async()=>structuredClone(s.response);await s.load();assert.equal(s.data.value.state,'healthy');assert.deepEqual(s.data.value.watches,[])
})
test('authorization and server failures do not masquerade as preview',async()=>{
 for(const code of ['http_401','http_403','http_500']){const s=harness();s.client.request=async()=>{throw new Error(code)};await s.load();assert.equal(s.data.value,null)}
})
const source=readFileSync(new URL('../src/views/TrialFollowupView.vue',import.meta.url),'utf8').match(/<script setup lang="ts">([\s\S]*?)<\/script>/)[1].replace(/^import .*\r?\n/gm,'')
const js=ts.transpileModule(source,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.None}}).outputText
function harness(){const filters={site_id:'a'},calls=[],errors=[];const data={site_id:'a',site_name:'A',phone_ready:true,display_name:'站点甲',watches:[],people:[{id:'a'.repeat(32),name:'运营',enabled:true,phone:'138****0000'}],events:[],worker_enabled:true,state:'healthy'};const client={request:async(url,options)=>{calls.push({url,...options});return structuredClone(data)}};const build=new Function('ref','computed','watch','useFiltersStore','useAuthStore','useRouter','ElMessage','ElMessageBox','client',js+';return {load,loadUsers,loadKeys,saveWatch,draft,data,mode,alias,editor,users,keys,identityError};');const ui=build(ref,computed,()=>{},()=>filters,()=>({}),()=>({}),{error:e=>errors.push(e),success:()=>{}},{confirm:async()=>{}},client);return {...ui,filters,calls,errors,client,response:data}}
test('trial phone requires an active selected recipient before writing',async()=>{const s=harness();await s.load();s.draft.value.user_id=7;s.draft.value.model='gpt-4.1';s.draft.value.label='客户';await s.saveWatch(true);assert.equal(s.calls.length,1);assert.match(s.errors[0],/已启用/);s.draft.value.person_ids=['a'.repeat(32)];await s.saveWatch(true);const write=s.calls.find(c=>c.body&&JSON.parse(c.body).action==='watch');const w=JSON.parse(write.body).watch;assert.equal(w.started_at,'0001-01-01T00:00:00Z');assert.equal(w.token_id,0);assert.equal(w.site,'a')})
test('two-step save snapshots site and payload before any await',async()=>{const s=harness();await s.load();s.draft.value.user_id=7;s.draft.value.model='gpt-4.1';s.draft.value.phone=false;s.draft.value.message=true;s.draft.value.label='客户甲';let release;const old=s.client.request;s.client.request=async(url,options)=>{if(options&&JSON.parse(options.body).action==='display_name'){s.calls.push({url,...options});await new Promise(r=>release=r);return {ok:true}}return old(url,options)};const saving=s.saveWatch(true);s.filters.site_id='b';s.draft.value.label='客户乙';s.draft.value.site='b';release();await saving;const write=s.calls.find(c=>c.body&&JSON.parse(c.body).action==='watch');assert.match(write.url,/site_id=a$/);assert.equal(JSON.parse(write.body).watch.label,'客户甲');assert.equal(JSON.parse(write.body).watch.site,'a')})
test('late site response cannot replace current trial list',async()=>{const s=harness();let release;s.client.request=()=>new Promise(r=>release=r);const pending=s.load();s.filters.site_id='b';release(s.response);await pending;assert.equal(s.data.value,null)})
test('late key directory response cannot replace another account',async()=>{const s=harness();const pending=[];s.client.request=()=>new Promise(r=>pending.push(r));s.draft.value.user_id=7;const first=s.loadKeys();s.draft.value.user_id=8;const second=s.loadKeys();pending[1]({items:[{id:80,name:'new'}]});await second;pending[0]({items:[{id:70,name:'old'}]});await first;assert.deepEqual(s.keys.value,[{id:80,name:'new'}])})
test('enabled watch requires a model and submits its exact trimmed name',async()=>{
 const s=harness();await s.load();s.draft.value.user_id=7;s.draft.value.phone=false;s.draft.value.message=true
 s.draft.value.model='  ';await s.saveWatch(true)
 assert.equal(s.calls.length,1);assert.match(s.errors[0],/测试模型/)
 s.draft.value.model=' GPT-4.1 ';await s.saveWatch(true)
 const write=s.calls.find(c=>c.body&&JSON.parse(c.body).action==='watch')
 assert.equal(JSON.parse(write.body).watch.model,'GPT-4.1')
})
