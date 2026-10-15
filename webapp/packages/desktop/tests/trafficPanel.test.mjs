import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import { computed, nextTick, reactive, ref, watch } from 'vue'

const sfc = readFileSync(new URL('../src/components/CustomerTrafficPanel.vue', import.meta.url), 'utf8')
const script = sfc.match(/<script setup lang="ts">([\s\S]*?)<\/script>/)[1].replace(/^import .*;\r?\n/gm, '')
const compiled = ts.transpileModule(script, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.None } }).outputText
function panel(withObserver = true) {
  const series = [{ key: '3', name: '甲', tokens: 1000, requests: 2, data: [] }, { key: '12', name: '乙', tokens: 9000, requests: 1, data: [] }]
  const props = reactive({ active: true, expanded: false, dimension: 'user', traffic: { metric: 'rpm', series, ranked: series, totalRequests: 3, totalTokens: 10000, coveredMinutes: 1 } })
  const mounted = [], unmounting = [], stops = [], observers = []
  class Observer {
    constructor(callback, options) { this.callback = callback; this.options = options; this.disconnected = false; observers.push(this) }
    observe(element) { this.element = element }
    disconnect() { this.disconnected = true }
  }
  const create = new Function('computed', 'ref', 'watch', 'onMounted', 'onBeforeUnmount', 'defineProps', 'defineEmits', 'IntersectionObserver', `${compiled}\nreturn {element, chartActive, selectedSeries, selectedKeys, search, listedItems, toggleSeries, metricLabel, totalCount, seriesCount};`)
  const state = create(computed, ref, (...args) => { const stop = watch(...args); stops.push(stop); return stop },
    callback => mounted.push(callback), callback => unmounting.push(callback), () => props, () => () => {}, withObserver ? Observer : undefined)
  state.element.value = {}
  mounted.forEach(callback => callback())
  return { ...state, props, observers,
    visible(value) { observers[0].callback([{ isIntersecting: value }]) },
    unmount() { unmounting.forEach(callback => callback()); stops.forEach(stop => stop()) },
  }
}

test('offscreen traffic panels defer chart drawing and render as the user scrolls into view', t => {
  const c = panel(); t.after(c.unmount)
  assert.equal(c.chartActive.value, false)
  assert.equal(c.props.traffic.series.length, 2)
  c.visible(true)
  assert.equal(c.chartActive.value, true)
  c.visible(false)
  assert.equal(c.chartActive.value, false)
  assert.equal(c.props.traffic.series.length, 2)
  assert.equal(c.observers[0].options.rootMargin, '400px 0px')
})

test('expanded panels render offscreen while inactive panels stay paused', t => {
  const c = panel(); t.after(c.unmount)
  c.props.expanded = true
  assert.equal(c.chartActive.value, true)
  c.props.active = false
  assert.equal(c.chartActive.value, false)
  c.visible(true)
  assert.equal(c.chartActive.value, false)
  c.props.active = true
  assert.equal(c.chartActive.value, true)
})

test('viewport and metric changes keep selected layers and search, but update counts and units', async t => {
  const c = panel(); t.after(c.unmount)
  c.props.expanded = true
  c.toggleSeries('3', false)
  c.search.value = '乙'
  c.visible(true); c.visible(false)
  assert.deepEqual(c.selectedSeries.value.map(item => item.key), ['12'])
  assert.equal(c.metricLabel.value, 'RPM')
  assert.equal(c.totalCount.value, 3)
  assert.equal(c.seriesCount(c.selectedSeries.value[0]), 1)
  c.props.traffic.metric = 'tpm'
  await nextTick()
  assert.equal(c.metricLabel.value, 'TPM')
  assert.equal(c.totalCount.value, 10000)
  assert.equal(c.seriesCount(c.selectedSeries.value[0]), 9000)
  assert.equal(c.search.value, '乙')
  assert.deepEqual(c.listedItems.value.map(item => item.key), ['12'])
  c.props.dimension = 'model'
  await nextTick()
  assert.equal(c.search.value, '')
  assert.equal(c.selectedSeries.value.length, 2)
})

test('observer is cleaned up on unmount and unsupported browsers retain chart rendering', () => {
  const c = panel()
  c.unmount()
  assert.equal(c.observers[0].disconnected, true)
  const fallback = panel(false)
  assert.equal(fallback.chartActive.value, true)
  assert.equal(fallback.observers.length, 0)
  fallback.unmount()
})
