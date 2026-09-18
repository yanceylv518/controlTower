import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
const compiled=ts.transpileModule(readFileSync(new URL('../src/utils/modelSquare.ts',import.meta.url),'utf8'),{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.CommonJS}}).outputText
const api={};new Function('exports',compiled)(api)
const model={quota_type:0,model_ratio:1.5,completion_ratio:4,cache_ratio:0.1}
test('NewAPI token and group ratios convert to USD per million',()=>{
 assert.equal(api.modelPrice(model,'input',0.5),1.5)
 assert.equal(api.modelPrice(model,'output',0.5),6)
 assert.ok(Math.abs(api.modelPrice(model,'cache')-0.3)<1e-10)
})
test('missing ratios remain unknown; actual zero prices remain zero',()=>{
 assert.equal(api.modelPrice(model,'write'),null)
 assert.equal(api.modelPrice({...model,model_ratio:null},'input'),null)
 assert.equal(api.modelPrice({...model,model_ratio:0},'output'),0)
 assert.equal(api.priceText(null),'—')
 assert.equal(api.priceText(0),'0')
})
test('per-request and expressions cannot be mistaken for token pricing',()=>{
 assert.equal(api.modelPrice({...model,quota_type:1,model_price:0.04},'input',2),0.08)
 assert.equal(api.modelPrice({...model,quota_type:1,model_price:0.04},'output'),null)
 assert.equal(api.modelPrice({...model,billing_expr:'tokens * 2'},'input'),null)
 assert.equal(api.modelPrice({...model,quota_type:9},'input'),null)
})
test('invalid and overflowing prices never display misleading numbers',()=>{
 for(const ratio of [-1,NaN,Infinity])assert.equal(api.modelPrice(model,'input',ratio),null)
 assert.equal(api.modelPrice({...model,model_ratio:1e308},'output'),null)
 assert.equal(api.modelPrice({...model,quota_type:1,model_price:1e308},'input',10),null)
})
