import assert from 'node:assert/strict'
import test from 'node:test'
import { readFileSync } from 'node:fs'
import { ref, shallowRef, computed } from 'vue'
import ts from 'typescript'

// 执行页面实际查询逻辑与实际异步状态管理器，通过受控 Promise 验证竞态而非依赖时间等待。
const source = readFileSync(new URL('../src/views/ReadonlyLogsView.vue', import.meta.url), 'utf8')
const asyncSource = readFileSync(new URL('../src/composables/useAsyncData.ts', import.meta.url), 'utf8').replace(/^import .*$/gm, '').replace('export function', 'function')
const compile = text => ts.transpileModule(text, {compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.CommonJS}}).outputText
const useAsyncData = new Function('ref','shallowRef','billingReadErrorMessage',compile(asyncSource)+';return useAsyncData')(ref,shallowRef,String)
const range = (start,end) => source.slice(source.indexOf(start),source.indexOf(end,source.indexOf(start)))
const code = range('const parsedChannelID =','// 首屏加载实例') + range('function commitQuery()', 'const pageSizeOptions =') + range('const changePage =','function closePageSizeMenu()')
const deferred = () => {let resolve,reject;const promise=new Promise((a,b)=>{resolve=a;reject=b});return {promise,resolve,reject}}
function setup() {
  const calls={logs:[],logStat:[],logCount:[]}, messages=[]
  const passthrough=Object.fromEntries(Object.keys(calls).map(name=>[name,(params,signal)=>{const d=deferred();calls[name].push({...d,params,signal});return d.promise}]))
  const deps={ref,shallowRef,computed,useAsyncData,passthrough,filters:{site_id:'a'},scopedUserIDs:ref(undefined),timeRange:ref([new Date('2026-09-01'),new Date('2026-09-02')]),logType:ref(0),limit:ref(100),offset:ref(0),closeRequestChain(){},ElMessage:{warning:m=>messages.push(m)},pageSizeOptions:[10,20,50,100]}
  for(const key of ['channelID','username','tokenName','modelName','group','requestID','upstreamRequestID'])deps[key]=ref('')
  const state=new Function(...Object.keys(deps),compile(code)+';return {state,statState,countState,refreshSearch,changePage,changePageSize,submitted,listIsCurrent,countIsCurrent,backgroundRefreshing,tableScroll} ')(...Object.values(deps))
  return {...deps,...state,calls,messages}
}
const response={items:[{id:1}],configured:true,total:1000,has_more:true}

test('pagination uses submitted filters and slow statistics do not block the list',async()=>{
  const h=setup();h.modelName.value='first';const refresh=h.refreshSearch()
  h.calls.logs[0].resolve(response);await refresh
  assert.equal(h.backgroundRefreshing.value,false)
  assert.equal(h.statState.loading.value,true)
  h.modelName.value='draft';h.changePage(2)
  assert.equal(h.calls.logs[1].params.model_name,'first')
  assert.equal(h.calls.logs[1].params.offset,100)
  h.calls.logs[1].resolve(response)
})

test('failed and superseded counts cannot appear as the current query total',async()=>{
  const h=setup();let job=h.refreshSearch();h.calls.logs[0].resolve(response);h.calls.logCount[0].resolve({total:1000});await job;await Promise.resolve()
  assert.equal(h.countIsCurrent.value,true)
  h.modelName.value='new';job=h.refreshSearch();assert.equal(h.countIsCurrent.value,false)
  h.calls.logs[1].resolve(response);h.calls.logCount[1].reject(new Error('count failed'));await job;await Promise.resolve()
  assert.equal(h.countIsCurrent.value,false)
  assert.ok(h.countState.error.value)
  h.calls.logStat[0].resolve({summary:{quota:999}});await Promise.resolve();await Promise.resolve()
  assert.equal(h.statState.data.value,undefined)
})

test('invalid channel IDs never issue a query or silently remove the filter',async()=>{
  for(const value of ['abc','-1','1.2','1e3','9007199254740992']){
    const h=setup();h.channelID.value=value;await h.refreshSearch();assert.equal(h.calls.logs.length,0);assert.equal(h.messages.length,1)
  }
})

test('failed searches preserve labelled old rows and prevent pagination under new conditions',async()=>{
  const h=setup();let job=h.refreshSearch();h.calls.logs[0].resolve(response);await job
  h.modelName.value='new';job=h.refreshSearch();h.calls.logs[1].reject(new Error('failed'));await job
  assert.equal(h.listIsCurrent.value,false);assert.equal(h.state.data.value.items[0].id,1)
  h.changePage(2);assert.equal(h.calls.logs.length,2)
})

test('pagination failures restore the shown page and successful pages reset only vertical scroll',async()=>{
  const h=setup();const job=h.refreshSearch();h.calls.logs[0].resolve(response);await job
  h.tableScroll.value={scrollTop:500,scrollLeft:150}
  h.changePage(2);h.calls.logs[1].reject(new Error('failed'));await new Promise(resolve=>setImmediate(resolve))
  assert.equal(h.offset.value,0);assert.equal(h.tableScroll.value.scrollTop,500)
  h.changePage(2);h.calls.logs[2].resolve(response);await new Promise(resolve=>setImmediate(resolve))
  assert.equal(h.offset.value,100);assert.equal(h.tableScroll.value.scrollTop,0);assert.equal(h.tableScroll.value.scrollLeft,150)
})
