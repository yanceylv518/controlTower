import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const source = readFileSync(new URL('../src/views/ModelSquareView.vue', import.meta.url), 'utf8')

test('model square copy uses the shared clipboard fallback', () => {
  assert.match(source, /import \{ copyText \} from '\.\.\/utils\/copyText'/)
  assert.match(source, /if \(await copyText\(name\)\) ElMessage\.success\('已复制模型名称'\)/)
  assert.match(source, /else ElMessage\.error\('复制失败，请手动复制模型名称'\)/)
  assert.doesNotMatch(source, /navigator\.clipboard\.writeText/)
})
