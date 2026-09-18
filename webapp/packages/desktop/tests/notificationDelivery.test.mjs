import assert from 'node:assert/strict'
import test from 'node:test'
import {readFileSync} from 'node:fs'
import ts from 'typescript'
const source=readFileSync(new URL('../src/utils/notificationDelivery.ts',import.meta.url),'utf8')
const compiled=ts.transpileModule(source,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ES2022}}).outputText
const {deliveryStatus,deliveryTone,retryTime}=await import('data:text/javascript;base64,'+Buffer.from(compiled).toString('base64'))
test('terminal deliveries never display a retry time',()=>{
 for(const status of ['sent','exhausted','expired']) assert.equal(retryTime({status,next_attempt_at:'9999-01-01T00:00:00Z'}),undefined)
 assert.equal(retryTime({status:'sent',next_attempt_at:'2026-09-19T00:00:00Z'}),undefined)
})
test('retrying deliveries show only a meaningful timestamp',()=>{
 for(const next_attempt_at of ['','invalid','0001-01-01T00:00:00Z','9999-01-01T00:00:00Z']) assert.equal(retryTime({status:'failed',next_attempt_at}),undefined)
 assert.equal(retryTime({status:'failed',next_attempt_at:'2026-09-19T00:00:00Z'}),'2026-09-19T00:00:00Z')
 assert.equal(deliveryStatus('failed'),'等待重试'); assert.equal(deliveryStatus('exhausted'),'投递失败')
 assert.equal(deliveryTone('exhausted'),'danger'); assert.equal(deliveryTone('sent'),'success')
})
