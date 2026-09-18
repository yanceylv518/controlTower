import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import ts from 'typescript'

// 使用真实静态资源测试品牌映射，避免把错误品牌或缺失 SVG 作为成功结果。
const compile = source => 'data:text/javascript;base64,' + Buffer.from(ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } }).outputText).toString('base64')
const iconsUrl = compile(readFileSync(new URL('../src/utils/modelProviderIcons.ts', import.meta.url), 'utf8'))
const source = readFileSync(new URL('../src/utils/modelProvider.ts', import.meta.url), 'utf8').replace("'./modelProviderIcons'", JSON.stringify(iconsUrl))
const { resolveModelProvider } = await import(compile(source))

test('rc35 model providers resolve namespaced and case-insensitive models to original brand SVGs', () => {
  for (const [model, name] of [['deepseek-v4-flash','DeepSeek'],['KIMI-K3','Moonshot'],['glm-5.3-flash','Zhipu'],['vendor/claude-sonnet-4','Claude'],['gemini-2.5-pro','Gemini'],['o3-mini','OpenAI'],['qwen3','Qwen'],['mimo-v2','XiaomiMiMo']]) {
    const provider = resolveModelProvider(model)
    assert.equal(provider.name, name)
    const svg = decodeURIComponent(provider.src.slice('data:image/svg+xml,'.length))
    assert.match(svg, /<svg[^>]*viewBox=/)
    assert.match(svg, /<path/)
    assert.doesNotMatch(svg, /<script|https?:\/\/(?!www.w3.org)/)
  }
  assert.notEqual(resolveModelProvider('deepseek-v4-flash').src, resolveModelProvider('kimi-k3').src)
})

test('unknown models never receive a fabricated provider icon', () => {
  for (const model of ['', 'custom-model', 'foo3-model', 'unknown']) assert.equal(resolveModelProvider(model), undefined)
})

test('monochrome provider assets retain currentColor while colored brands retain their colors', () => {
  for (const model of ['kimi-k3','gpt-4.1','grok-3','mimo-v2','step-2']) {
    assert.match(decodeURIComponent(resolveModelProvider(model).src), /currentColor/)
  }
  assert.match(decodeURIComponent(resolveModelProvider('deepseek-v4-flash').src), /#4D6BFE/)
  const component = readFileSync(new URL('../src/components/ModelProviderIcon.vue', import.meta.url), 'utf8')
  assert.match(component, /color:var\(--ct-ink\)/)
  assert.match(component, /hasOwnProperty.call\(modelProviderIcons, props.name\)/)
})
