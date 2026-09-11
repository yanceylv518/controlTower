import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
const source = readFileSync(new URL('../src/utils/logTaskRefresh.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } }).outputText
const { mergeLogTaskRefresh } = await import(`data:text/javascript;base64,${Buffer.from(compiled).toString('base64')}`)
test('transient failure preserves progress, other source advances, retry recovers', () => {
 const a={id:'a',result:{status:'running',lines:['existing']}}
 const b={id:'b',result:{status:'running',lines:[]}}
 const done={...b,result:{status:'succeeded',lines:['new']}}
 const first=mergeLogTaskRefresh([a,b],[a,b],[{status:'rejected',reason:{status:500}},{status:'fulfilled',value:done}])
 assert.deepEqual(first.tasks,[a,done]);assert.match(first.warning,/自动重试/)
 const recovered={...a,result:{status:'succeeded',lines:['existing','last']}}
 const next=mergeLogTaskRefresh(first.tasks,[a],[{status:'fulfilled',value:recovered}])
 assert.deepEqual(next.tasks,[recovered,done]);assert.equal(next.warning,'')
})
test('404 terminates pending task without discarding previous lines',()=>{
 const a={id:'a',result:{status:'running',lines:['existing']}}
 const next=mergeLogTaskRefresh([a],[a],[{status:'rejected',reason:{status:404}}])
 assert.equal(next.tasks[0].result.status,'failed');assert.deepEqual(next.tasks[0].result.lines,['existing'])
})
