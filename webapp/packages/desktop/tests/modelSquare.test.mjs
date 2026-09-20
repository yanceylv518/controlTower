import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
const compiled=ts.transpileModule(readFileSync(new URL('../src/utils/modelSquare.ts',import.meta.url),'utf8'),{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.CommonJS}}).outputText
const api={};new Function('exports',compiled)(api)
const model={quota_type:0,model_ratio:1.5,completion_ratio:4,cache_ratio:0.1}
test('site display currency converts real prices without silently defaulting to USD',()=>{
 assert.equal(api.displayPrice(3,{type:'CNY',symbol:'¥',price_multiplier:7.2}),'21.6')
 assert.equal(api.displayPrice(3,{type:'CUSTOM',symbol:'HK$',price_multiplier:7.8}),'23.4')
 assert.equal(api.currencyUnit({type:'CUSTOM',symbol:'HK$'}),'HK$')
 assert.equal(api.currencyUnit({type:'TOKENS',symbol:''}),'额度')
 assert.equal(api.displayPrice(3,{type:'TOKENS',price_multiplier:500000}),'1,500,000')
 assert.equal(api.displayPrice(0,{type:'USD',price_multiplier:1}),'0')
 for(const currency of [null,{price_multiplier:NaN},{price_multiplier:0},{price_multiplier:-1}]) assert.equal(api.displayPrice(3,currency),'—')
 assert.equal(api.displayPrice(1e308,{price_multiplier:7}),'—')
})
test('expression tiers expose literal token, cache, media and fixed request prices',()=>{
 const tiers=api.expressionTiers('v1:len <= 200000 ? tier("standard", p * 3 + c * 15 + cr * 0.3 + cc * 0 + ai * 2e+1) : tier("long", p * 6 + c * 22.5)')
 assert.equal(tiers.length,2)
 assert.deepEqual(tiers[0].prices.map(p=>p.value),[3,15,0.3,0,20])
 assert.equal(tiers[1].prices[0].value,6)
 assert.equal(api.displayPrice(tiers[0].prices[0].value*0.5,{price_multiplier:7.2}),'10.8')
 const fixed=api.expressionTiers('tier("request", fixed(0.01))')[0]
 assert.equal(fixed.unit,'次')
 assert.equal(fixed.prices[0].value,0.01)
})
test('complex or invalid expression arithmetic is not misrepresented as a literal price',()=>{
 for(const raw of ['p * 2 / 10','p * 2 * 3','p * 2 + max(c * 3, 1)','u("seconds") * 2','p * -1','p * 1e999','p * 2 + p * 3','p * 2 +']) {
   assert.deepEqual(api.expressionTiers(`tier("custom", ${raw})`)[0].prices,[],raw)
 }
 assert.deepEqual(api.expressionTiers('tier("broken", p * 2'),[])
 assert.deepEqual(api.expressionTiers('tokens * 2'),[])
 assert.deepEqual(api.expressionTiers('tier("bad\\q", p * 2)'),[])
})
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
