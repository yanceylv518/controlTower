import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import ts from 'typescript'

const source = readFileSync(new URL('../src/components/CustomerTrafficChart.vue', import.meta.url), 'utf8')
const script = source.split('<script setup lang="ts">')[1].split('</script>')[0].replace(/^import .*;\r?$/gm, '')
const compiled = ts.transpileModule(script, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.None } }).outputText
function setup() {
  const actions = [], options = [], events = {}
  const props = { series: [{ key: '7', name: 'Alice', color: '#4170cd', data: [[1, 10]] }, { key: '8', name: 'Bob', color: '#16a6b6', data: [[1, 20]] }] }
  const chart = { on: (name, fn) => { events[name] = fn }, dispatchAction: action => actions.push(action), setOption: option => options.push(option) }
  const context = vm.createContext({
    useTheme: () => ({}), defineProps: () => props, defineExpose() {}, ref: () => ({}), watch() {}, onBeforeUnmount() {},
    echarts: { use() {}, init: () => chart, graphic: { LinearGradient: class {} } },
    LineChart: {}, GridComponent: {}, TooltipComponent: {}, SVGRenderer: {},
    withChartTheme: option => option, getComputedStyle: () => ({ getPropertyValue: () => '' }),
    trafficRGBA: color => color, formatTokens: String, highlightCustomerTooltip() {},
  })
  vm.runInContext(compiled, context)
  vm.runInContext('element.value = {}; renderNow()', context)
  return { actions, options, events, context, props }
}

test('all stacked layers stay visible and refresh clears legend emphasis before and after redraw', () => {
  const h = setup()
  assert.ok(h.options[0].series.every(series => series.emphasis.focus === 'none' && series.areaStyle.opacity === 1))
  vm.runInContext('highlight("7")', h.context)
  assert.equal(h.actions.at(-1).type, 'highlight')
  h.actions.length = 0
  h.props.series.reverse()
  vm.runInContext('renderNow()', h.context)
  assert.deepEqual(h.actions.map(action => action.type), ['downplay', 'downplay'])
  assert.equal(vm.runInContext('activeKey', h.context), undefined)
})

test('chart exit and legend exit clear emphasis without dropping layers', () => {
  const h = setup()
  for (const exit of [() => h.events.mouseout(), () => h.events.globalout(), () => vm.runInContext('highlight()', h.context), () => vm.runInContext('resetHighlight()', h.context)]) {
    vm.runInContext('highlight("8")', h.context)
    exit()
    assert.equal(h.actions.at(-1).type, 'downplay')
    assert.equal(vm.runInContext('activeKey', h.context), undefined)
    assert.equal(h.options.at(-1).series.length, 2)
  }
  assert.ok(source.includes('@mouseleave="resetHighlight"'))
})
