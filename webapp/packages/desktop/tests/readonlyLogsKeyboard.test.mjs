import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import ts from 'typescript'

const source = readFileSync(new URL('../src/views/ReadonlyLogsView.vue', import.meta.url), 'utf8')
const handler = source.slice(source.indexOf('function handlePageSearchKeydown('), source.indexOf('const fallbackTotal ='))
const code = ts.transpileModule(handler, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText

function setup() {
  const state = { loading: { value: false } }
  const backgroundRefreshing = { value: false }, detailOpen = { value: false }, affinityOpen = { value: false }, pageSizeOpen = { value: false }
  let searches = 0
  const overlays = []
  class Element {
    constructor(selector) { this.selector = selector }
    closest(selectors) { return selectors.split(', ').includes(this.selector) ? this : null }
  }
  const handle = new Function('state', 'backgroundRefreshing', 'detailOpen', 'affinityOpen', 'pageSizeOpen', 'Element', 'document', 'search', code + '; return handlePageSearchKeydown;')(
    state, backgroundRefreshing, detailOpen, affinityOpen, pageSizeOpen, Element,
    { querySelectorAll: () => overlays }, () => { searches++ },
  )
  const event = (selector = 'body', extra = {}) => ({ key: 'Enter', target: new Element(selector), preventDefault() { this.defaultPrevented = true }, ...extra })
  return { handle, event, state, backgroundRefreshing, detailOpen, affinityOpen, pageSizeOpen, overlays, get searches() { return searches } }
}

test('Enter on the page or table triggers one search and is registered only while mounted', () => {
  const p = setup()
  for (const selector of ['body', 'td']) {
    const event = p.event(selector)
    p.handle(event)
    assert.equal(event.defaultPrevented, true)
  }
  assert.equal(p.searches, 2)
  assert.match(source, /window.addEventListener\('keydown', handlePageSearchKeydown\)/)
  assert.match(source, /window.removeEventListener\('keydown', handlePageSearchKeydown\)/)
})

test('page shortcut leaves inputs, native actions, IME and modified keys alone', () => {
  const p = setup()
  for (const selector of ['input', 'textarea', 'select', 'button', 'a', '[role="combobox"]', '[role="dialog"]', '[role="menu"]', '[role="listbox"]']) p.handle(p.event(selector))
  for (const extra of [{ key: 'Escape' }, { defaultPrevented: true }, { isComposing: true }, { keyCode: 229 }, { repeat: true }, { ctrlKey: true }, { altKey: true }, { metaKey: true }, { shiftKey: true }]) p.handle(p.event('body', extra))
  assert.equal(p.searches, 0)
})

test('loading and open overlays suppress page search, hidden overlays do not', () => {
  const p = setup()
  for (const flag of [p.state.loading, p.backgroundRefreshing, p.detailOpen, p.affinityOpen, p.pageSizeOpen]) {
    flag.value = true
    p.handle(p.event())
    flag.value = false
  }
  p.overlays.push({ getClientRects: () => [{}] })
  p.handle(p.event())
  assert.equal(p.searches, 0)
  p.overlays[0] = { getClientRects: () => [] }
  p.handle(p.event())
  assert.equal(p.searches, 1)
})
