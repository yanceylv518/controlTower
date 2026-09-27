import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import { computed, ref } from 'vue'
const compile = source => ts.transpileModule(source,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.CommonJS}}).outputText
const moneyAPI = {}
new Function('exports',compile(readFileSync(new URL('../src/utils/archiveMoney.ts',import.meta.url),'utf8')))(moneyAPI)
const api = {}
new Function('exports','require',compile(readFileSync(new URL('../src/utils/archiveInsights.ts',import.meta.url),'utf8')))(api,()=>moneyAPI)
const { anomalySummary, percent } = api
const historicalPrices=rows=>api.historicalPrices(rows,{site_id:'a',type:'USD',symbol:'$',raw_quota_per_unit:'500000',exchange_rate:'1',observed_at:'2026-09-27T00:00:00Z'})
const hour = (overrides={}) => ({hour:'0',log_rows:'8',consumption:'6',empty_output:'3',missing_output:'2',error:'1',charged_empty_output:'2',...overrides})
test('hourly categories stay disjoint, exact totals and empty hours remain explicit',()=>{
 const result=anomalySummary([hour(),hour({hour:'23'})])
 assert.equal(result.total.log_rows,16n);assert.equal(result.total.empty_output,6n)
 assert.equal(result.total.missing_output,4n);assert.equal(result.total.error,2n)
 assert.equal(result.hours.length,24);assert.equal(result.hours[1].log_rows,0n)
 assert.equal(percent(1n,3n),'33.33%');assert.equal(percent(0n,0n),'—')
 const big=anomalySummary([hour({log_rows:'9007199254741000',consumption:'9007199254740993'})])
 assert.equal(big.total.consumption,9007199254740993n)
 assert.equal(anomalySummary([]).total.log_rows,0n)
})
test('malformed/inconsistent aggregates fail instead of producing plausible totals',()=>{
 for(const rows of [[hour(),hour()],[hour({hour:'24'})],[hour({empty_output:'7'})],[hour({charged_empty_output:'4'})],[hour({error:'3'})],[hour({error:'-1'})],[hour({consumption:9007199254740993})]])assert.throws(()=>anomalySummary(rows))
})
const stat=(pricing,changes={})=>({date:'2026-07-04',dimensions:{type:'2',model_name:'m',pricing},amounts:{requests:'2'},...changes})
test('historical price variants retain discounts, zero prices and observation dates',()=>{
 const rows=[stat({model_ratio:'0.5',completion_ratio:3,group_ratio:1}),stat({group_ratio:1,completion_ratio:3,model_ratio:'0.5'},{date:'2026-07-06'}),stat({model_ratio:'0.5',completion_ratio:3,group_ratio:0.5})]
 const result=historicalPrices(rows)
 assert.equal(result.length,2);const combined=result.find(r=>r.requests===4n)
 assert.equal(combined.first,'2026-07-04');assert.equal(combined.last,'2026-07-06')
 assert.equal(combined.input,'1');assert.equal(combined.output,'3');assert.equal(combined.cache,'—')
 assert.equal(historicalPrices([stat({model_ratio:0,completion_ratio:0,model_price:0})])[0].input,'0')
 assert.equal(historicalPrices([stat({model_price:'0.02'})])[0].request,'0.02')
 assert.equal(historicalPrices([stat({model_ratio:1e-7})])[0].input,'0.0000002')
})
test('expression and absent prices do not invent a token price or use current configuration',()=>{
 const expr=historicalPrices([stat({model_ratio:2,billing_expr:'unsafe()',group_ratio:0.5})])[0]
 assert.equal(expr.input,'—');assert.match(expr.mode,/表达式/);assert.match(expr.evidenceText,/unsafe/)
 assert.equal(historicalPrices([stat(undefined)])[0].mode,'缺少历史价格')
 assert.equal(historicalPrices([stat({model_ratio:-1})])[0].input,'—')
 assert.equal(historicalPrices([stat({}, {dimensions:{type:5}})]).length,0)
})
class ApiError extends Error { constructor(status,code){super(code);this.status=status;this.code=code} }
const errorAPI={}
new Function('exports','require',compile(readFileSync(new URL('../src/utils/archiveReadError.ts',import.meta.url),'utf8')))(errorAPI,()=>({ApiError}))
const {archiveReadError}=errorAPI
function overview(){
 const script=readFileSync(new URL('../src/components/ArchiveAnomalyOverview.vue',import.meta.url),'utf8').split('<script setup lang="ts">')[1].split('</script>')[0].replace(/^import .*$/gm,'')
 const props={query:{site_id:'a',date:'2026-07-04'}},requests=[],client={request:url=>new Promise((resolve,reject)=>requests.push({url,resolve,reject}))}
 const js=compile(script)
 const c=new Function('computed','ref','watch','onUnmounted','defineProps','client','ApiError','archiveReadError','anomalySummary','percent',js+';return {load,data,error,busy}') (computed,ref,()=>{},()=>{},()=>props,client,ApiError,archiveReadError,anomalySummary,percent)
 return {...c,props,requests}
}
test('overview ignores stale site results and rejects partial aggregate pages',async()=>{
 const c=overview(),old=c.load();c.props.query={site_id:'b',date:'2026-07-05'};const fresh=c.load()
 c.requests[1].resolve({items:[hour()],has_more:false});await fresh
 c.requests[0].resolve({items:[hour({error:'0'})],has_more:false});await old
 assert.equal(c.data.value.total.error,1n);assert.match(c.requests[1].url,/site_id=b/)
 const partial=c.load();c.requests[2].resolve({items:[hour()],has_more:true});await partial
 assert.equal(c.data.value,undefined);assert.match(c.error.value,/不完整/)
 const retry=c.load();c.requests[3].resolve({items:[],has_more:false});await retry
 assert.equal(c.data.value.total.log_rows,0n);assert.equal(c.error.value,'')
})

test('overview reports timeout without blaming server version',async()=>{
 const c=overview(),pending=c.load()
 c.requests[0].reject(new ApiError(503,'archive_read_timeout'));await pending
 assert.match(c.error.value,/超时/);assert.doesNotMatch(c.error.value,/升级/)
 assert.equal(c.data.value,undefined);assert.equal(c.busy.value,false)
})

test('price display removes far-tail noise without erasing small prices',()=>{
 const c={site_id:'a',type:'USD',symbol:'$',raw_quota_per_unit:'1000000',exchange_rate:'1',observed_at:'2026-09-27T00:00:00Z'}
 assert.equal(moneyAPI.historicalPrice(c,'token','1.300000000002'),'1.3')
 assert.equal(moneyAPI.historicalPrice(c,'token','3','0.008333333333'),'0.025')
 assert.equal(moneyAPI.historicalPrice(c,'token','0.000000000000123456789'),'0.000000000000123456789')
 assert.equal(moneyAPI.historicalPrice(c,'token','1.234567891234'),'1.234567891')
 assert.equal(moneyAPI.historicalPrice(c,'token','10'),'10')
})
