import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'

const source = readFileSync(new URL('../src/utils/customerTooltip.ts', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText
const api = {}; new Function('exports', compiled)(api)
const place = api.customerTooltipPosition

test('tooltip stays outside chart and stationary as pointer moves through it', () => {
  const chart = { left: 220, top: 350, width: 400, height: 190 }
  const a = place(chart, [10, 20], [280, 250], [1440, 900])
  const b = place(chart, [390, 180], [280, 250], [1440, 900])
  assert.deepEqual(a, b)
  assert.equal(a.outside, true)
  assert.ok(a.position[0] >= chart.width)
})

test('rightmost chart uses the left side, bottom row stays inside viewport', () => {
  const chart = { left: 990, top: 750, width: 400, height: 190 }
  const p = place(chart, [200, 100], [300, 320], [1440, 900])
  assert.equal(p.outside, true)
  assert.ok(p.position[0] + 300 <= 0)
  assert.ok(chart.top + p.position[1] + 320 <= 892)
})

test('expanded chart uses space above or below without covering the plot', () => {
  const chart = { left: 200, top: 350, width: 1100, height: 320 }
  const above = place(chart, [500, 100], [340, 250], [1440, 900])
  assert.equal(above.outside, true)
  assert.ok(above.position[1] + 250 <= 0)
  const below = place({ ...chart, top: 50 }, [500, 100], [340, 250], [1440, 900])
  assert.equal(below.outside, true)
  assert.ok(below.position[1] >= chart.height)
})

test('constrained viewport uses opposite corner with pointer-through fallback', () => {
  const chart = { left: 8, top: 20, width: 374, height: 740 }
  const p = place(chart, [10, 20], [330, 320], [390, 780])
  assert.equal(p.outside, false)
  const x = chart.left + p.position[0], y = chart.top + p.position[1]
  assert.ok(x >= 8 && x + 330 <= 382 && y >= 8 && y + 320 <= 772)
  assert.ok(y > chart.top + 20)
})

test('moving vertically in cached tooltip changes the focused row without changing order', () => {
  const rows = ['model-b', 'model-a', 'model-c'].map((key, i) => ({ dataset: { trafficKey: key, trafficColor: '#7b63cd' }, style: {}, offsetTop: i * 30, offsetHeight: 30 }));
  const root = { querySelectorAll: () => rows, scrollTop: 0, clientHeight: 60 };
  api.highlightCustomerTooltip(root, 'model-a');
  assert.equal(rows[1].style.fontWeight, '700');
  assert.equal(rows[0].style.fontWeight, '400');
  assert.equal(rows[0].style.opacity, '1');
  assert.equal(rows[0].style.color, 'var(--ct-ink-3)');
  assert.equal(rows[1].style.opacity, '1');
  api.highlightCustomerTooltip(root, 'model-c');
  assert.equal(rows[1].style.backgroundColor, 'transparent');
  assert.equal(rows[2].dataset.active, 'true');
  assert.equal(rows[1].style.opacity, '1');
  assert.equal(rows[1].style.color, 'var(--ct-ink-3)');
  assert.equal(rows[2].style.opacity, '1');
  assert.equal(rows[2].style.borderLeftColor, '#7b63cd');
  assert.equal(root.scrollTop, 30);
  assert.deepEqual(rows.map(row => row.dataset.trafficKey), ['model-b', 'model-a', 'model-c']);
  api.highlightCustomerTooltip(root);
  assert.ok(rows.every(row => row.style.fontWeight === '400' && row.dataset.active === 'false' && row.style.opacity === '1'));
  api.highlightCustomerTooltip(root, 'missing-model');
  assert.ok(rows.every(row => row.style.opacity === '1'));
});

test('model identity with punctuation and numeric channel identity match exactly', () => {
  const rows = ['provider:model-"x"', '3', '33'].map(key => ({ dataset: { trafficKey: key }, style: {}, offsetTop: 0, offsetHeight: 20 }));
  const root = { querySelectorAll: () => rows, scrollTop: 0, clientHeight: 100 };
  api.highlightCustomerTooltip(root, '3');
  assert.deepEqual(rows.map(row => row.dataset.active), ['false', 'true', 'false']);
  api.highlightCustomerTooltip(root, 'provider:model-"x"');
  assert.deepEqual(rows.map(row => row.dataset.active), ['true', 'false', 'false']);
});
