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

// 回归保护：候选先用当前页结果即时显示，远端搜索仍取消旧请求并保持短防抖。
test('user name picker shows local options immediately and cancels stale searches', () => {
  assert.match(componentSource, /options\.value = localSuggestions\(keyword\)/)
  assert.match(componentSource, /setTimeout\(\(\) => \{ void searchUsers\(keyword\) \}, keyword \? 100 : 0\)/)
  assert.match(componentSource, /keyword \? mergeUserSuggestions\(remote, local\) : mergeUserSuggestions\(local, remote\)/)
  assert.match(componentSource, /activeController\?\.abort\(\)/)
  assert.match(componentSource, /sequence !== searchSequence/)
  assert.match(componentSource, /userSuggestionCache\.set\(cacheKey\(keyword\), remote\)/)
  assert.match(apiSource, /users: \(params: \{[\s\S]*?\}, signal\?: AbortSignal\)/)
  assert.match(apiSource, /requestOptions\(signal\)/)
})

test('loading and remote errors do not hide local user choices', () => {
  assert.match(componentSource, /loading && !options\.length/)
  assert.match(componentSource, /searchError && !options\.length/)
  assert.match(componentSource, /远端搜索失败，当前显示已有日志中的用户/)
  assert.match(componentSource, /aria-live="polite"/)
  assert.match(componentSource, /:aria-busy="loading"/)
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
