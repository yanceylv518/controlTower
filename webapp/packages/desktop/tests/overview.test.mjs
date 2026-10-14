import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import ts from 'typescript';
import { computed, reactive, ref, watch, nextTick } from 'vue';

const compile = source => ts.transpileModule(source, {compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.CommonJS}}).outputText;
const utils = new Function('exports', compile(readFileSync(new URL('../src/utils/overview.ts', import.meta.url),'utf8'))+';return exports;')({});
const sfc = readFileSync(new URL('../src/views/OverviewView.vue', import.meta.url),'utf8');
const script = compile(sfc.match(/<script setup lang="ts">([\s\S]*?)<\/script>/)[1].replace(/^import .*;\r?\n/gm,''));
const pending = () => { let resolve,reject; const promise=new Promise((a,b)=>{resolve=a;reject=b}); return {promise,resolve,reject}; };
function page() {
  const filters=reactive({site_id:'a',loadInstances:async()=>{}}), calls=[];
  const fetch=(section,site,...rest)=>{const p=pending();calls.push({...p,section,site,signal:rest.at(-1)});return p.promise;};
  const dashboard={overviewRequests:(...a)=>fetch('requests',...a),overviewRuntime:(...a)=>fetch('runtime',...a),overviewAlerts:(...a)=>fetch('alerts',...a)};
  let teardown;
  const context={computed,reactive,ref,watch,nextTick,onMounted:()=>{},onBeforeUnmount:fn=>{teardown=fn},echarts:{use(){}},LineChart:{},GridComponent:{},TooltipComponent:{},LegendComponent:{},CanvasRenderer:{},useFiltersStore:()=>filters,useAuthStore:()=>({user:{role:'admin'}}),useTheme:()=>({themeSignature:ref('light')}),dashboard,...utils,canVisit:()=>true,formatBeijingTime:v=>v,formatNumber:v=>v,formatPercent:v=>v,formatSeconds:v=>v,withChartTheme:v=>v,document:{hidden:false,removeEventListener(){}}};
  const p=new Function(...Object.keys(context),script+';ready=true;return {refresh,requests,runtime,alerts,loading,errors,requestNotice,alertFresh,hours};')(...Object.values(context));
  return {...p,filters,calls,teardown:()=>teardown()};
}
const data = (site) => ({site,queried_at:new Date().toISOString(),sources:['node'],recent:{from:'2026-10-14T00:00:00Z',to:'2026-10-14T00:01:00Z',complete:true,metrics:{request_count:10}},points:[],items:[],fresh:true,evaluated_at:new Date().toISOString()});
test('TPM trend uses token totals, normalizes window duration and leaves gaps',()=>{
  const point={from:'2026-10-14T00:00:00Z',to:'2026-10-14T00:05:00Z',complete:true,metrics:{request_count:100,tpm:50000,success_rate:.04}};
  assert.deepEqual(utils.overviewTrend(point),{tpm:10000,success:4});
  assert.deepEqual(utils.overviewTrend({...point,to:'2026-10-14T00:01:00Z'}),{tpm:50000,success:4});
  assert.deepEqual(utils.overviewTrend({...point,complete:false}),{tpm:null,success:null});
  assert.deepEqual(utils.overviewTrend({...point,metrics:null}),{tpm:null,success:null});
  assert.deepEqual(utils.overviewTrend({...point,metrics:{request_count:0,tpm:0,success_rate:null}}),{tpm:0,success:null});
});
test('freshness rejects missing, stale and future evidence',()=>{
  const now=Date.now();
  for(const v of [null,'invalid',new Date(now-121000).toISOString(),new Date(now+6000).toISOString()]) assert.equal(utils.overviewFresh(v,now),false);
  assert.equal(utils.overviewFresh(new Date(now).toISOString(),now),true);
});
test('requests render before slow runtime and failed alerts; errors stay isolated',async()=>{
  const p=page();const run=p.refresh();
  p.calls.find(x=>x.section==='requests').resolve(data('a'));
  p.calls.find(x=>x.section==='alerts').reject(new Error('failure'));
  await new Promise(resolve=>setImmediate(resolve));
  assert.equal(p.requests.value.site,'a');assert.equal(p.loading.requests,false);assert.equal(p.loading.runtime,true);assert.ok(p.errors.alerts);assert.equal(p.errors.requests,'');
  p.calls.find(x=>x.section==='runtime').resolve(data('a'));await run;p.teardown();
});
test('site change aborts prior load; late success and failure cannot overwrite new site',async()=>{
  const p=page();const old=p.refresh();const first=p.calls.slice();
  p.filters.site_id='b';await nextTick();const second=p.calls.slice(3);
  assert.equal(first[0].signal.aborted,true);assert.equal(p.requests.value,null);
  second.forEach(c=>c.resolve(data('b')));await new Promise(resolve=>setImmediate(resolve));
  first[0].resolve(data('a'));first[1].reject(new Error('late'));first[2].resolve(data('a'));await old;
  assert.equal(p.requests.value.site,'b');assert.equal(p.runtime.value.site,'b');assert.equal(p.errors.runtime,'');p.teardown();
});
test('background failure retains previous values with explicit warning',async()=>{
  const p=page();const first=p.refresh();p.calls.forEach(c=>c.resolve(data('a')));await first;
  const old=p.requests.value;const run=p.refresh(true);p.calls.slice(3).forEach(c=>c.reject(new Error('offline')));await run;
  assert.equal(p.requests.value,old);assert.match(p.requestNotice.value,/保留上次数据/);assert.equal(p.alertFresh.value,false);p.teardown();
});
test('loader timeout aborts work and superseded completion does not release new owner',async()=>{
  const loader=utils.overviewLoader(10), old=loader.start(), current=loader.start();
  old.finish();assert.equal(loader.busy,true);assert.equal(old.current(),false);
  await new Promise(resolve=>setTimeout(resolve,20));assert.equal(current.signal.aborted,true);
  current.finish();assert.equal(loader.busy,false);
});
