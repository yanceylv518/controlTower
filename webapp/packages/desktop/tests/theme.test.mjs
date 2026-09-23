import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import ts from 'typescript'
import * as vue from 'vue'

const css = readFileSync(new URL('../src/theme.css', import.meta.url), 'utf8')
const blocks = [...css.matchAll(/:root(?:\[data-theme="dark"\])?\s*\{([^}]+)\}/g)]
const tokens = block => Object.fromEntries([...block.matchAll(/--ct-([\w-]+):\s*(#[\da-f]{6});/gi)].map(m => [m[1], m[2]]))
const palettes = { light: tokens(blocks[0][1]), dark: { ...tokens(blocks[0][1]), ...tokens(blocks[1][1]) } }
const variants = readFileSync(new URL('../src/palettes.css', import.meta.url), 'utf8')
for (const name of ['graphite', 'jade', 'violet']) {
  const shared = tokens(variants.match(new RegExp(`:root\\[data-palette="${name}"\\] \\{([^}]+)\\}`))[1])
  for (const mode of ['light', 'dark']) {
    const block = variants.match(new RegExp(`:root\\[data-palette="${name}"\\]\\[data-theme="${mode}"\\] \\{([^}]+)\\}`))[1]
    palettes[`${name}-${mode}`] = { ...palettes[mode], ...tokens(block), ...shared }
  }
}
const importedPresets = readFileSync(new URL('../src/newapi-palettes.css', import.meta.url), 'utf8')
const presetNames = []
for (const m of importedPresets.matchAll(/:root\[data-palette="([^"]+)"\]\[data-theme="(light|dark)"\]\s*\{([^}]+)\}/g)) {
  palettes[`${m[1]}-${m[2]}`] = { ...palettes[m[2]], ...tokens(m[3]) }
  if(m[2]==='light')presetNames.push(m[1])
}
function luminance(hex) {
  const a = [1, 3, 5].map(i => parseInt(hex.slice(i, i + 2), 16) / 255).map(v => v <= .04045 ? v / 12.92 : ((v + .055) / 1.055) ** 2.4)
  return a[0] * .2126 + a[1] * .7152 + a[2] * .0722
}
const contrast = (a, b) => (Math.max(luminance(a), luminance(b)) + .05) / (Math.min(luminance(a), luminance(b)) + .05)
const mix = (a, b, weight) => '#' + [1, 3, 5].map(i => Math.round(parseInt(a.slice(i, i + 2), 16) * weight + parseInt(b.slice(i, i + 2), 16) * (1 - weight)).toString(16).padStart(2, '0')).join('')
for (const [name, palette] of Object.entries(palettes)) {
  test(`${name}: decorative gradient samples preserve text contrast`, () => {
    for (let step = 0; step <= 10; step++) {
      const progress = step / 10
      for (const fg of ['ink', 'ink-2', 'ink-3']) {
        assert.ok(contrast(palette[fg], mix(palette['accent-weak'], palette.surface, .3 * progress)) >= 4.5, `${fg} on card tint`)
        assert.ok(contrast(palette[fg], mix(palette['accent-weak'], palette.bg, .42 * progress)) >= 4.5, `${fg} on canvas tint`)
      }
      assert.ok(contrast(palette['sidebar-ink'], mix(palette['sidebar-hover'], palette.sidebar, .32 * progress)) >= 4.5, 'sidebar tint')
      assert.ok(contrast(palette['on-solid'], mix(palette['primary-solid'], palette['primary-hover'], progress)) >= 4.5, 'primary gradient')
    }
  })
  test(`${name}: normal, secondary and semantic text meet 4.5:1`, () => {
    for (const bg of ['surface', 'surface-2', 'bg']) for (const fg of ['ink', 'ink-2', 'ink-3', 'accent', 'ok', 'warn', 'crit', 'purple']) {
      assert.ok(contrast(palette[fg], palette[bg]) >= 4.5, `${fg} on ${bg}: ${contrast(palette[fg], palette[bg]).toFixed(2)}`)
    }
    for (const fg of ['accent', 'ok', 'warn', 'crit', 'purple']) assert.ok(contrast(palette[fg], palette[fg + '-weak']) >= 4.5, `${fg} badge`)
  })
  test(`${name}: solid buttons, navigation and logs meet 4.5:1`, () => {
    for (const bg of ['primary-solid', 'primary-hover', 'success-solid', 'warning-solid', 'danger-solid', 'info-solid']) assert.ok(contrast(palette['on-solid'], palette[bg]) >= 4.5, bg)
    for (const bg of ['sidebar', 'sidebar-hover', 'sidebar-selected']) assert.ok(contrast(palette['sidebar-ink'], palette[bg]) >= 4.5, bg)
    assert.ok(contrast(palette['log-ink'], palette['log-bg']) >= 4.5)
  })
}

function compile(file, globals = {}) {
  const source = readFileSync(new URL(file, import.meta.url), 'utf8')
  const js = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText
  const exports = {}
  vm.runInNewContext(js, { exports, require: () => vue, ...globals })
  return exports
}
function harness(saved = null, dark = false, storageBroken = false, savedPalette = null, savedFont = null) {
  const events = {}, root = { dataset: {}, style: {}, classList: { toggle(name, value) { this[name] = value } } }
  const media = { matches: dark, addEventListener(name, fn) { events.media = fn } }
  const storage = { getItem(key) { if (storageBroken) throw Error('blocked'); return key === 'ct.theme.font' ? savedFont : key === 'ct.theme.palette' ? savedPalette : saved }, setItem(key, value) { if (storageBroken) throw Error('blocked'); if (key === 'ct.theme.font') savedFont = value; else if (key === 'ct.theme.palette') savedPalette = value; else saved = value } }
  const globals = { document: { documentElement: root }, window: { matchMedia: () => media, addEventListener(name, fn) { events[name] = fn } }, localStorage: storage }
  return { ...globals, root, media, events, read: () => saved, api: compile('../src/composables/useTheme.ts', globals) }
}
test('preference persists; system changes only affect system mode; cross-tab updates apply', () => {
  const h = harness(null, true)
  h.api.initializeTheme()
  assert.equal(h.root.dataset.theme, 'dark')
  h.api.setTheme('light')
  assert.equal(h.read(), 'light')
  h.events.media()
  assert.equal(h.root.dataset.theme, 'light')
  h.api.setTheme('system')
  assert.equal(h.root.dataset.theme, 'dark')
  h.media.matches = false; h.events.media()
  assert.equal(h.root.dataset.theme, 'light')
  h.events.storage({ key: 'ct.theme', newValue: 'dark' })
  assert.equal(h.root.dataset.theme, 'dark')
  h.events.storage({ key: 'ct.theme', newValue: null })
  assert.equal(h.root.dataset.theme, 'light')
  h.events.storage({ key: null, newValue: null })
  assert.equal(h.api.useTheme().preference.value, 'system')
})
test('blocked storage and invalid preferences never break initialization or switching', () => {
  for (const h of [harness('invalid', true), harness(null, true, true)]) {
    h.api.initializeTheme(); h.api.initializeTheme()
    assert.equal(h.root.dataset.theme, 'dark')
    h.api.setTheme('light')
    assert.equal(h.root.style.colorScheme, 'light')
    assert.equal(h.root.classList.dark, false)
  }
})
test('font selection persists independently and triggers chart redraws', () => {
  const h = harness('dark', false, false, 'jade', 'yahei')
  h.api.initializeTheme()
  const state = h.api.useTheme()
  assert.equal(h.root.dataset.font, 'yahei')
  h.api.setFont('dengxian')
  assert.equal(state.themeSignature.value, 'dark:jade:dengxian')
  assert.equal(h.localStorage.getItem('ct.theme.font'), 'dengxian')
  h.events.storage({ key: 'ct.theme.font', newValue: 'simsun' })
  assert.equal(state.themeSignature.value, 'dark:jade:simsun')
  h.api.setFont('invalid')
  assert.equal(h.root.dataset.font, 'simsun')
  h.api.setTheme('light'); h.api.setPalette('violet')
  assert.equal(h.root.dataset.font, 'simsun')
  h.events.storage({ key: 'ct.theme.font', newValue: null })
  assert.equal(state.themeSignature.value, 'light:violet:system')
  h.api.setFont('yahei'); h.events.storage({ key: null, newValue: null })
  assert.equal(h.root.dataset.font, 'system')
  const blocked = harness(null, false, true)
  blocked.api.initializeTheme(); blocked.api.setFont('simsun')
  assert.equal(blocked.root.dataset.font, 'simsun')
})
test('palette is independent, persisted, cross-tab synchronized and invalidation reaches charts', () => {
  const h = harness('dark', false, false, 'graphite')
  h.api.initializeTheme()
  const state = h.api.useTheme()
  assert.equal(h.root.dataset.palette, 'graphite')
  assert.equal(state.themeSignature.value, 'dark:graphite:system')
  h.api.setPalette('jade')
  assert.equal(h.root.dataset.theme, 'dark')
  assert.equal(state.themeSignature.value, 'dark:jade:system')
  assert.equal(h.localStorage.getItem('ct.theme.palette'), 'jade')
  h.api.setTheme('light')
  assert.equal(state.themeSignature.value, 'light:jade:system')
  h.events.storage({ key: 'ct.theme.palette', newValue: 'violet' })
  assert.equal(state.themeSignature.value, 'light:violet:system')
  h.api.setPalette('invalid')
  assert.equal(h.root.dataset.palette, 'violet')
  h.events.storage({ key: 'ct.theme.palette', newValue: null })
  assert.equal(state.themeSignature.value, 'light:default:system')
  h.events.storage({ key: null, newValue: null })
  assert.equal(state.preference.value, 'system')
  assert.equal(state.palette.value, 'default')
  const blocked = harness(null, true, true)
  blocked.api.initializeTheme(); blocked.api.setPalette('violet')
  assert.equal(blocked.root.dataset.palette, 'violet')
})
test('prepaint script and mounted app resolve the same theme', () => {
  const boot = readFileSync(new URL('../public/theme-init.js', import.meta.url), 'utf8')
  for (const saved of [null, 'dark', 'light', 'system', 'invalid']) for (const dark of [true, false]) for (const palette of ['blue', 'graphite', 'jade', 'violet', 'invalid']) for (const font of ['system', 'yahei', 'dengxian', 'simsun', 'invalid']) {
    const h = harness(saved, dark, false, palette, font)
    vm.runInNewContext(boot, { document: h.document, localStorage: h.localStorage, matchMedia: () => h.media })
    const first = h.root.dataset.theme, firstPalette = h.root.dataset.palette, firstFont = h.root.dataset.font
    h.api.initializeTheme()
    assert.equal(h.root.dataset.theme, first)
    assert.equal(h.root.dataset.palette, firstPalette)
    assert.equal(h.root.dataset.font, firstFont)
  }
})
test('charts keep data/formatters, update axes and tooltip, and ensure series contrast', () => {
  for (const palette of Object.values(palettes)) {
    const api = compile('../src/utils/chartTheme.ts', { document: { documentElement: {} }, getComputedStyle: () => ({ fontFamily: 'DengXian, sans-serif', getPropertyValue: key => palette[key.slice(5)] }) })
    for (const color of ['#2f6fed', '#16a6b6', '#7357d8', '#e47b22', '#36a269', '#8390a5']) assert.ok(contrast(api.chartColor(color), palette.surface) >= 3)
    const formatter = v => `${v}%`, data = [1, 2, 3]
    const source = { color: ['#2f6fed'], tooltip: { trigger: 'axis', formatter }, xAxis: { type: 'time' }, yAxis: { axisLabel: { formatter } }, legend: {}, series: [{ type: 'line', data }] }
    const themed = api.withChartTheme(source)
    assert.equal(themed.series[0].data, data)
    assert.equal(themed.tooltip.formatter, formatter)
    assert.equal(themed.tooltip.textStyle.fontFamily, 'DengXian, sans-serif')
    assert.equal(themed.xAxis.axisLabel.fontFamily, 'DengXian, sans-serif')
    assert.equal(themed.yAxis.axisLabel.formatter, formatter)
    assert.equal(themed.tooltip.backgroundColor, palette.surface)
    assert.equal(themed.xAxis.axisLabel.color, palette['ink-3'])
    assert.equal(source.xAxis.axisLabel, undefined)
  }
})

test('hashed avatars retain identity backgrounds with white initials', () => {
  const api = compile('../src/utils/identityColors.ts')
  for (let i = 0; i < 1000; i++) {
    const name = `客户-${i}`, style = api.getUserAvatarStyle(name)
    assert.deepEqual(style, api.getUserAvatarStyle(name))
    assert.equal(style.color, '#ffffff')
    assert.match(style.backgroundColor, /^hsl\(\d+ \d+% \d+%\)$/)
  }
})

test('all ten presets persist and match first paint in both modes', () => {
 assert.equal(presetNames.length,10)
 const script=readFileSync(new URL('../public/theme-init.js',import.meta.url),'utf8')
 for(const name of presetNames)for(const dark of [false,true]) {
  const h=harness('system',dark,false,name)
  const pre={dataset:{},style:{},classList:{toggle(){}}}
  vm.runInNewContext(script,{document:{documentElement:pre},localStorage:h.localStorage,matchMedia:()=>({matches:dark})})
  h.api.initializeTheme()
  assert.equal(h.root.dataset.palette,name)
  assert.equal(pre.dataset.palette,name)
  assert.equal(pre.dataset.theme,h.root.dataset.theme)
  assert.equal(pre.style.backgroundColor,palettes[`${name}-${dark?'dark':'light'}`].bg)
  h.api.setPalette(name)
  assert.equal(h.localStorage.getItem('ct.theme.palette'),name)
 }
})
