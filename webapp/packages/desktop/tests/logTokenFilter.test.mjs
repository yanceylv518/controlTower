import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import ts from 'typescript'
import { computed, effectScope, nextTick, reactive, ref, watch } from 'vue'

const compile = source => ts.transpileModule(source, { compilerOptions: { target:ts.ScriptTarget.ES2022, module:ts.ModuleKind.CommonJS } }).outputText
const utils = {}
new Function('exports', compile(readFileSync(new URL('../src/utils/logTokenFilter.ts', import.meta.url), 'utf8')))(utils)
const provided = value => Object.fromEntries(Object.entries(utils.logTokenFilterParams(value)).filter(([,bound]) => bound !== undefined))
const filter = extra => ({ ...utils.emptyLogTokenFilter(), ...extra })
const source = readFileSync(new URL('../src/components/LogTokenFilter.vue', import.meta.url), 'utf8')
const script = source.match(/<script setup lang="ts">([\s\S]*?)<\/script>/)[1].replace(/^import .*$/gm, '')

function panel(t, extra = {}) {
  const props = reactive({ modelValue:filter({}), disabled:false, ...extra }), events = []
  const scope = effectScope()
  t.after(() => scope.stop())
  const deps = { computed, nextTick, reactive, ref, watch, useId:()=>'token-test', defineProps:()=>props, withDefaults:value=>value, defineEmits:()=>(...args)=>events.push(args), ...utils }
  const state = scope.run(() => new Function(...Object.keys(deps), compile(script) + ';return {draft,open,error,trigger,prepare,close,apply,clear,handleCountEnter};')(...Object.values(deps)))
  return { props, events, ...state }
}

test('one Tokens value always requests an exact count in all three modes, including zero', () => {
  for (const [extra, expected] of [
    [{input:' 032 '}, {prompt_tokens_min:32,prompt_tokens_max:32}],
    [{mode:'output',output:'0'}, {completion_tokens_min:0,completion_tokens_max:0}],
    [{mode:'output',output:'10'}, {completion_tokens_min:10,completion_tokens_max:10}],
    [{mode:'both',input:'100',output:'200'}, {prompt_tokens_min:100,prompt_tokens_max:100,completion_tokens_min:200,completion_tokens_max:200}],
    [{input:String(Number.MAX_SAFE_INTEGER)}, {prompt_tokens_min:Number.MAX_SAFE_INTEGER,prompt_tokens_max:Number.MAX_SAFE_INTEGER}],
  ]) {
    const value = filter(extra)
    assert.equal(utils.validateLogTokenFilter(value), '')
    assert.deepEqual(provided(value), expected)
    assert.equal(utils.hasLogTokenFilter(value), true)
  }
  assert.deepEqual(provided(filter({})), {})
  assert.equal(utils.hasLogTokenFilter(filter({mode:'both',input:' ',output:' '})), false)
})

test('invalid and unsafe counts cannot be applied', () => {
  for (const mode of ['input','output','both']) {
    for (const raw of ['-1','1.5','1e3','Infinity','NaN','9007199254740992','1 OR 1=1','１０']) {
      const key = mode === 'output' ? 'output' : 'input'
      assert.match(utils.validateLogTokenFilter(filter({mode,[key]:raw})), /必须是/)
    }
  }
  assert.match(utils.validateLogTokenFilter(filter({mode:'both',output:'-1'})), /输出 Tokens 必须是/)
})

test('switching modes retains drafts and only validates and sends visible dimensions', () => {
  const value = filter({mode:'output',input:'invalid',output:'20'})
  assert.equal(utils.validateLogTokenFilter(value), '')
  assert.deepEqual(provided(value), {completion_tokens_min:20,completion_tokens_max:20})
  value.mode = 'both'
  assert.match(utils.validateLogTokenFilter(value), /输入/)
  value.input = '0';value.mode = 'input'
  assert.deepEqual(provided(value), {prompt_tokens_min:0,prompt_tokens_max:0})
  assert.equal(value.output, '20')
})

test('summaries describe exact counts and unrestricted dimensions', () => {
  assert.equal(utils.logTokenFilterSummary(filter({})), 'Tokens 不限')
  assert.equal(utils.logTokenFilterSummary(filter({mode:'output',output:'0'})), '输出 = 0')
  assert.equal(utils.logTokenFilterSummary(filter({input:'1000'})), '输入 = 1,000')
  assert.equal(utils.logTokenFilterSummary(filter({mode:'both',input:'100',output:'0'})), '输入 = 100 · 输出 = 0')
  assert.equal(utils.logTokenFilterSummary(filter({mode:'both',input:'100'})), '输入 = 100 · 输出 不限')
})

test('cancelling discards edits; reopening restores the current parent value', async t => {
  const p = panel(t, {modelValue:filter({mode:'output',output:'10'})})
  p.open.value = true;p.prepare();p.draft.output = '20';p.close()
  assert.equal(p.props.modelValue.output, '10');assert.deepEqual(p.events, [])
  p.props.modelValue = filter({mode:'both',input:'100',output:'30'})
  await nextTick();p.prepare()
  assert.equal(p.draft.output, '30');assert.equal(p.draft.input, '100')
})

test('applying rejects invalid counts and emits an independent snapshot on success', async t => {
  const p = panel(t)
  p.open.value = true;p.draft.input = '-1';await nextTick();p.apply()
  assert.match(p.error.value, /必须是/);assert.equal(p.open.value, true);assert.deepEqual(p.events, [])
  p.draft.input = '32';await nextTick();p.apply()
  assert.equal(p.open.value, false);assert.equal(p.events.length, 1)
  p.draft.input = '40';assert.equal(p.events[0][1].input, '32')
})

test('clearing removes both counts, and disabled controls cannot emit changes', t => {
  const p = panel(t, {modelValue:filter({mode:'both',input:'1',output:'2'})})
  p.clear();assert.deepEqual(p.events, [['update:modelValue',utils.emptyLogTokenFilter()]])
  p.props.disabled = true;p.apply();p.clear();assert.equal(p.events.length, 1)
})

test('Enter confirms without leaking a page search, while IME composition remains untouched', async t => {
  const p = panel(t)
  p.draft.input = '0';await nextTick()
  const event = extra => ({prevented:false,stopped:false,preventDefault(){this.prevented=true},stopPropagation(){this.stopped=true},...extra})
  for (const extra of [{isComposing:true},{keyCode:229}]) {
    const e = event(extra);p.handleCountEnter(e)
    assert.equal(e.prevented, false);assert.equal(e.stopped, false)
  }
  const e = event({});p.handleCountEnter(e)
  assert.equal(e.prevented, true);assert.equal(e.stopped, true);assert.equal(p.events.length, 1)
  assert.equal(p.events[0][1].input, '0')
})

test('the shared API forwards exact counts and zero to list, count and stat', async () => {
  const apiModule = {}
  new Function('exports', compile(readFileSync(new URL('../../shared/src/api/passthrough.ts', import.meta.url), 'utf8')))(apiModule)
  const calls = [], api = apiModule.passthroughApi({request:(...args)=>{calls.push(args);return Promise.resolve({})}})
  const signal = new AbortController().signal
  const params = {site:'test',...utils.logTokenFilterParams(filter({mode:'both',input:'0',output:'20'}))}
  for (const name of ['logs','logCount','logStat']) await api[name](params, signal)
  for (const [path, options] of calls) {
    const query = new URL(path,'http://localhost').searchParams
    assert.equal(query.get('prompt_tokens_min'), '0');assert.equal(query.get('completion_tokens_max'), '20')
    assert.equal(query.get('prompt_tokens_max'), '0');assert.equal(query.get('completion_tokens_min'), '20')
    assert.equal(options.signal, signal)
  }
})
