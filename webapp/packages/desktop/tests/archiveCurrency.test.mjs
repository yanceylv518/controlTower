import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import { computed, ref, watch, nextTick, effectScope } from 'vue'
const compile=source=>ts.transpileModule(source,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.CommonJS}}).outputText
const moneyAPI={}
new Function('exports',compile(readFileSync(new URL('../src/utils/archiveMoney.ts',import.meta.url),'utf8')))(moneyAPI)
const {parseArchiveCurrency,quotaAmount,currencyUnit,historicalPrice,moneyContext}=moneyAPI
const cny={site_id:'a',type:'CNY',symbol:'¥',raw_quota_per_unit:'500000',exchange_rate:'7.2',observed_at:'2026-09-27T01:02:03Z'}
test('quota, historical token and per-request price share actual site currency',()=>{
 const c=parseArchiveCurrency(cny,'a')
 assert.equal(quotaAmount(500000n,c),'7.200000');assert.equal(historicalPrice(c,'token','0.5'),'7.2')
 assert.equal(historicalPrice(c,'token','0.5','3'),'21.6');assert.equal(historicalPrice(c,'request','0.1'),'0.72')
 assert.equal(currencyUnit(c),'CNY ¥');assert.match(moneyContext(c),/不是历史汇率/)
 const custom={...c,type:'CUSTOM',symbol:'HK$',exchange_rate:'7.8',raw_quota_per_unit:'1000000'}
 assert.equal(quotaAmount(1000000n,custom),'7.800000');assert.equal(historicalPrice(custom,'token','0.5'),'3.9')
 const tokens={...c,type:'TOKENS',symbol:''}
 assert.equal(quotaAmount(9007199254740993n,tokens),'9007199254740993')
 assert.equal(historicalPrice(tokens,'token','0.5'),'500000');assert.equal(historicalPrice(tokens,'request','0.1'),'50000')
 assert.equal(quotaAmount(0n,c),'0.000000');assert.equal(historicalPrice(c,'token',0),'0')
})
test('exact conversion retains large integers and rounds once at display',()=>{
 const c={...cny,type:'USD',symbol:'$',raw_quota_per_unit:'1',exchange_rate:'1'}
 assert.equal(quotaAmount(9007199254740993n,c),'9007199254740993.000000')
 assert.equal(quotaAmount(1n,{...c,raw_quota_per_unit:'3'}),'0.333333')
 assert.equal(quotaAmount(1n,{...c,raw_quota_per_unit:'2000000'}),'0.000001')
 assert.equal(quotaAmount(1n,{...c,raw_quota_per_unit:'1e6',exchange_rate:'2.5'}),'0.000003')
 assert.equal(quotaAmount(5n,undefined),'—');assert.equal(historicalPrice(undefined,'token',1),'—')
})
test('unavailable, old, cross-site and invalid configuration never fall back to USD',()=>{
 for(const raw of [{...cny,site_id:'b'},{...cny,raw_quota_per_unit:500000},{...cny,exchange_rate:'0'},{...cny,type:'WHAT'},{...cny,observed_at:'bad'},{...cny,raw_quota_per_unit:'NaN'}, {type:'CNY',quota_per_unit:500000/7.2,price_multiplier:7.2}])assert.throws(()=>parseArchiveCurrency(raw,'a'))
})
test('site switch clears conversion and ignores stale errors, retry changes currency',async()=>{
 const requests=[],site=ref('a'),client={request:url=>{const q=new URL(url,'http://localhost').searchParams;assert.equal(q.get('site'),site.value);assert.equal(q.has('site_id'),false);return new Promise((resolve,reject)=>requests.push({url,resolve,reject}))}}
 const source=readFileSync(new URL('../src/composables/useArchiveCurrency.ts',import.meta.url),'utf8').replace(/^import .*$/gm,'')
 const api={};let dispose
 new Function('exports','ref','watch','onUnmounted','client','parseArchiveCurrency',compile(source))(api,ref,watch,fn=>dispose=fn,client,parseArchiveCurrency)
 const scope=effectScope();const c=scope.run(()=>api.useArchiveCurrency(()=>site.value))
 const old=c.refreshMoney();site.value='b';await nextTick();const current=c.refreshMoney()
 requests[1].resolve({...cny,site_id:'b',type:'CUSTOM',symbol:'HK$',exchange_rate:'7.8'});await current
 requests[0].reject(new Error('old'));await old;assert.equal(c.money.value.symbol,'HK$');assert.equal(c.moneyError.value,'')
 const failed=c.refreshMoney();assert.equal(c.money.value,undefined);requests[2].reject(new Error('unavailable'));await failed
 assert.equal(c.money.value,undefined);assert.match(c.moneyError.value,/金额不可用/)
 const retry=c.refreshMoney();requests[3].resolve({...cny,site_id:'b',type:'TOKENS',symbol:''});await retry
 assert.equal(c.money.value.type,'TOKENS');assert.equal(c.moneyError.value,'')
 dispose();scope.stop()
})
test('statistics CSV exports converted amounts and exact conversion evidence',()=>{
 const utils={};new Function('exports',compile(readFileSync(new URL('../src/utils/archiveAnalysis.ts',import.meta.url),'utf8')))(utils)
 const script=readFileSync(new URL('../src/components/ArchiveUsageStatistics.vue',import.meta.url),'utf8').split('<script setup lang="ts">')[1].split('</script>')[0].replace(/^import .*$/gm,'')
 const money=ref(cny),scope={computed,ref,watch:()=>{},onUnmounted:()=>{},defineProps:()=>({siteId:'a'}),ApiError:Error,client:{},beijingDate:()=> '2026-09-27',...utils,...moneyAPI,useArchiveCurrency:()=>({money,moneyError:ref(''),moneyBusy:ref(false),refreshMoney:async()=>{}})}
 const c=new Function(...Object.keys(scope),compile(script)+';return {rows,days,versions,csvText,series}')(...Object.values(scope))
 c.rows.value=[{date:'2026-09-01',dimensions:{type:2,model_name:'=formula'},amounts:{quota:'500000',requests:'1'}}];c.days.value=['2026-09-01'];c.versions.value={'2026-09-01':'version-1'}
 const csv=c.csvText();assert.match(csv,/7\.200000/);assert.match(csv,/CNY ¥/);assert.match(csv,/"7.2"/);assert.match(csv,/2026-09-27T01:02:03Z/);assert.match(csv,/not_historical_fx/);assert.match(csv,/"'=formula"/)
 assert.equal(c.series.value[0].data[0][1],7.2)
 money.value=undefined;assert.match(c.csvText(),/currency_unavailable/);assert.equal(c.series.value[0].data[0][1],null)
})
