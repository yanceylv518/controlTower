import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const componentSource = readFileSync(
  new URL('../src/components/UserNamePicker.vue', import.meta.url),
  'utf8'
).replace(/\r\n/g, '\n')
const apiSource = readFileSync(
  new URL('../../shared/src/api/passthrough.ts', import.meta.url),
  'utf8'
)

// 回归保护：用户搜索必须防抖并取消旧请求，避免输入过程产生竞态和额外连接。
test('user name picker debounces and cancels stale searches', () => {
  assert.match(componentSource, /setTimeout\(\(\) => \{ void searchUsers\(keyword\) \}, 240\)/)
  assert.match(componentSource, /activeController\?\.abort\(\)/)
  assert.match(componentSource, /sequence !== searchSequence/)
  assert.match(apiSource, /users: \(params: \{[\s\S]*?\}, signal\?: AbortSignal\)/)
  assert.match(apiSource, /requestOptions\(signal\)/)
})

// 回归保护：选择用户传递精确 ID，手动输入和清空则解除上次选择。
test('user name picker exposes selection and keyboard interactions', () => {
  assert.match(componentSource, /emit\('select', option\)/)
  assert.match(componentSource, /emit\('select', null\)/)
  assert.match(componentSource, /role="combobox"/)
  assert.match(componentSource, /role="listbox"/)
  assert.match(componentSource, /role="option"/)
  assert.match(componentSource, /event\.key === 'ArrowDown'/)
  assert.match(componentSource, /event\.key === 'ArrowUp'/)
  assert.match(componentSource, /event\.key === 'Escape'/)
  assert.match(componentSource, /@pointerdown\.prevent="selectOption\(option\)"/)
})

// 回归保护：Teleport 到 body 后仍需跟随 Control Tower 全局主题，并适应窄视口。
test('user name picker dropdown follows responsive theme tokens', () => {
  assert.match(componentSource, /background: var\(--ct-surface/)
  assert.match(componentSource, /box-shadow: var\(--ct-shadow-popover/)
  assert.match(componentSource, /max-height: min\(320px, calc\(100vh - 16px\)\)/)
  assert.match(componentSource, /const viewportWidth = Math\.max\(0, window\.innerWidth - margin \* 2\)/)
  assert.match(componentSource, /@media \(max-width: 560px\)/)
})
