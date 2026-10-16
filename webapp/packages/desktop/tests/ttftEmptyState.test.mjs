import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import { computed, effectScope, markRaw, nextTick, reactive, ref, watch } from 'vue'

const component = readFileSync(new URL('../src/components/CustomerCompareChart.vue', import.meta.url), 'utf8')
const script = component.match(/<script setup lang="ts">([\s\S]*?)<\/script>/)[1].replace(/^import .*;\r?\n/gm, '')
const compiled = ts.transpileModule(script, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText
const view = readFileSync(new URL('../src/views/DimensionView.vue', import.meta.url), 'utf8')
const explanationSource = view.match(/function ttftEmptyState\([\s\S]*?(?=\r?\nfunction metricHeadline)/)[0]
const explanation = new Function(`${ts.transpileModule(explanationSource, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText}\nreturn ttftEmptyState;`)()
const point = value => ['2026-10-16T10:00:00Z', value]
const series = value => ['P50', 'P90', 'P95'].map(name => ({ name, data: [point(value)] }))
const settle = async () => { await nextTick(); await Promise.resolve(); await nextTick() }

function chart(overrides = {}) {
  const props = reactive({ series: [], unit: 's', thresholds: [], compact: false, ...overrides })
  const scope = effectScope(), unmounting = [], instances = [], observers = [], queued = new Map()
  const themeSignature = ref('light')
  class Observer {
    constructor(callback) { this.callback = callback; this.disconnected = false; observers.push(this) }
    observe(element) { this.element = element }
    disconnect() { this.disconnected = true }
  }
  const echarts = {
    use() {},
    init(element) {
      const instance = { element, options: [], disposed: 0, resize() {}, setOption(option) { this.options.push(option) }, dispose() { this.disposed++ } }
      instances.push(instance)
      return instance
    },
  }
  const runtime = {
    exports: {}, computed, nextTick, ref, watch, echarts, ResizeObserver: Observer,
    defineProps: () => props, withDefaults: value => value,
    onBeforeUnmount: callback => unmounting.push(callback), useTheme: () => ({ themeSignature }), withChartTheme: value => value,
    scheduleChartRender: (token, render) => queued.set(token, render), cancelChartRender: token => queued.delete(token),
    LineChart: {}, DataZoomComponent: {}, GridComponent: {}, LegendComponent: {}, MarkLineComponent: {}, TooltipComponent: {}, CanvasRenderer: {},
  }
  const create = new Function('runtime', `const {${Object.keys(runtime).join(',')}} = runtime;\n${compiled}\nreturn {hasData, chartEl, render, renderNow};`)
  const state = scope.run(() => create(runtime))
  return { ...state, props, instances, observers, queued, themeSignature,
    mount(id) { state.chartEl.value = markRaw({ id }) },
    flush() { for (const [token, render] of [...queued]) { queued.delete(token); render() } },
    unmount() { unmounting.forEach(callback => callback()); state.chartEl.value = undefined; scope.stop() },
  }
}

for (const [name, rows, expected] of [
  ['no selected series', [], false],
  ['three empty series', ['P50', 'P90', 'P95'].map(name => ({ name, data: [] })), false],
  ['three null series', series(null), false],
  ['non-finite values', [{ name: 'P95', data: [point(NaN), point(Infinity), point(-Infinity)] }], false],
  ['measured zero', series(0), true],
  ['measured latency', series(.45), true],
  ['one valid point among gaps', [{ name: 'P95', data: [point(null), point(.9), point(null)] }], true],
]) {
  test(`chart data presence: ${name}`, t => {
    const c = chart({ series: rows }); t.after(c.unmount)
    assert.equal(c.hasData.value, expected)
  })
}

test('all-null series do not create a chart even if a previous canvas reference exists', async t => {
  const c = chart({ series: series(null) }); t.after(c.unmount)
  c.mount('old-canvas')
  await settle(); c.flush(); c.renderNow()
  assert.equal(c.instances.length, 0)
})

test('data to empty to data disposes the old chart and draws into the new canvas', async t => {
  const c = chart({ series: series(.9) }); t.after(c.unmount)
  c.mount('first')
  await settle(); c.flush()
  assert.equal(c.instances.length, 1)
  assert.equal(c.instances[0].element.id, 'first')
  assert.deepEqual(c.instances[0].options[0].series.map(item => item.name), ['P50', 'P90', 'P95'])
  c.props.series = series(null)
  c.chartEl.value = undefined
  await settle(); c.flush()
  assert.equal(c.instances[0].disposed, 1)
  assert.equal(c.observers[0].disconnected, true)
  assert.equal(c.queued.size, 0)
  c.props.series = series(0)
  c.mount('second')
  await settle(); c.flush()
  assert.equal(c.instances.length, 2)
  assert.equal(c.instances[1].element.id, 'second')
  assert.equal(c.instances[1].disposed, 0)
  assert.deepEqual(c.instances[1].options[0].series[0].data, [point(0)])
})

test('a stale queued render cannot initialize an empty chart', async t => {
  const c = chart({ series: series(.9) }); t.after(c.unmount)
  c.mount('canvas'); await settle()
  const staleRender = [...c.queued.values()][0]
  assert.equal(typeof staleRender, 'function')
  c.props.series = series(null)
  await settle()
  staleRender()
  assert.equal(c.instances.length, 0)
  c.chartEl.value = undefined
  await settle()
  assert.equal(c.queued.size, 0)
})

test('theme and metric changes keep rendering on the current canvas', async t => {
  const c = chart({ series: series(.9) }); t.after(c.unmount)
  c.mount('canvas'); await settle(); c.flush()
  const first = c.instances[0]
  c.themeSignature.value = 'dark'
  await settle(); c.flush()
  c.props.unit = '%'; c.props.series = [{ name: '缓存命中率', data: [point(40)] }]
  await settle(); c.flush()
  assert.equal(c.instances.length, 1)
  assert.equal(first.disposed, 0)
  assert.equal(first.options.at(-1).yAxis.max, 100)
  assert.equal(first.options.at(-1).series[0].name, '缓存命中率')
})

test('unmount cancels pending draws and disconnects the resize observer', async () => {
  const c = chart({ series: series(.9) })
  c.mount('canvas'); await settle(); c.flush()
  c.props.series = series(1)
  await settle()
  assert.equal(c.queued.size, 1)
  c.unmount()
  assert.equal(c.queued.size, 0)
  assert.equal(c.instances[0].disposed, 1)
  assert.equal(c.observers[0].disconnected, true)
})

for (const [name, row, text, hint] of [
  ['no requests', { request_count: 0, stream_rate: null }, '所选时段暂无请求', '有流式请求'],
  ['zero requests and zero stream ratio', { request_count: 0, stream_rate: 0 }, '所选时段暂无请求', '有流式请求'],
  ['only non-streaming requests', { request_count: 100, stream_rate: 0 }, '所选时段无流式请求', 'TPM 或 OTPS'],
  ['streaming without latency', { request_count: 100, stream_rate: .2 }, '暂无 TTFT 样本', '已记录流式请求'],
  ['unknown stream ratio', { request_count: 100, stream_rate: null }, '暂无 TTFT 样本', 'TTFT 需要流式请求'],
  ['legacy stream ratio missing', { request_count: 100 }, '暂无 TTFT 样本', 'TTFT 需要流式请求'],
  ['summary absent', undefined, '暂无 TTFT 样本', 'TTFT 需要流式请求'],
]) {
  test(`TTFT empty explanation: ${name}`, () => {
    const result = explanation(row)
    assert.equal(result.text, text)
    assert.ok(result.hint.includes(hint))
  })
}
