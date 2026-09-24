import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import ts from 'typescript'

const source = readFileSync(new URL('../src/utils/logPickerSuggestions.ts', import.meta.url), 'utf8')
const userPickerSource = readFileSync(new URL('../src/components/UserNamePicker.vue', import.meta.url), 'utf8')
const compiled = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText
const module = { exports: {} }
new Function('module', 'exports', compiled)(module, module.exports)
const { filterUserSuggestions, mergeUserSuggestions, SuggestionCache } = module.exports

test('user suggestions match display name, username and numeric ID without duplicates', () => {
  const users = [
    { id: 12, username: 'alice', display_name: 'Alice Chen' },
    { id: 34, username: 'bob', display_name: 'Bob' },
  ]
  assert.deepEqual(filterUserSuggestions(users, 'CHEN'), [users[0]])
  assert.deepEqual(filterUserSuggestions(users, '34'), [users[1]])
  assert.deepEqual(mergeUserSuggestions(users, [{ ...users[0], display_name: '' }]), users)
})

test('suggestion cache expires entries and evicts least-recently-used keys', () => {
  let now = 100
  const cache = new SuggestionCache(2, 50, () => now)
  cache.set('a', ['one'])
  cache.set('b', ['two'])
  assert.deepEqual(cache.get('a'), ['one'])
  cache.set('c', ['three'])
  assert.equal(cache.get('b'), undefined)
  now = 151
  assert.equal(cache.get('a'), undefined)
})

test('user picker renders local choices while remote search continues', () => {
  assert.match(userPickerSource, /options\.value = localSuggestions\(keyword\)/)
  assert.match(userPickerSource, /SuggestionCache/)
  assert.match(userPickerSource, /loading && !options\.length/)
  assert.match(userPickerSource, /searchError && !options\.length/)
  assert.match(userPickerSource, /keyword \? 100 : 0/)
})
