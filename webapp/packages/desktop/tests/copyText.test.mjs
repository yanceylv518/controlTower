import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
const source = readFileSync(new URL('../src/utils/copyText.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 } }).outputText
const { copyText } = await import(`data:text/javascript;base64,${Buffer.from(compiled).toString('base64')}`)

for (const scenario of ['https', 'http', 'denied', 'unsupported']) {
  test(`clipboard ${scenario} preserves multiline text and reports actual result`, async () => {
    const text = 'Agent A / app.log\n中文 PPIO\nAgent B\nsecond line'
    const saved = new Map(['window', 'navigator', 'document'].map(k => [k, Object.getOwnPropertyDescriptor(globalThis, k)]))
    let copied, selected = false, removed = false, restored = false
    const field = { style: {}, focus() {}, select() { selected = true }, remove() { removed = true } }
    const window = { isSecureContext: scenario !== 'http', getSelection: () => null }
    const navigator = { clipboard: scenario === 'http' ? undefined : { async writeText(value) { if (scenario !== 'https') throw new Error('denied'); copied = value } } }
    const document = {
      activeElement: { focus() { restored = true } },
      createElement: () => field, body: { appendChild() {} },
      execCommand(command) { assert.equal(command, 'copy'); assert.equal(selected, true); copied = field.value; return scenario !== 'unsupported' },
    }
    try {
      for (const [key, value] of Object.entries({ window, navigator, document })) Object.defineProperty(globalThis, key, { value, configurable: true })
      assert.equal(await copyText(text), scenario !== 'unsupported')
      assert.equal(copied, text)
      if (scenario !== 'https') { assert.equal(removed, true); assert.equal(restored, true) }
    } finally {
      for (const [key, descriptor] of saved) { if (descriptor) Object.defineProperty(globalThis, key, descriptor); else delete globalThis[key] }
    }
  })
}
