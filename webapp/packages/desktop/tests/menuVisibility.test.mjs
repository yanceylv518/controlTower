import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import vm from 'node:vm'
import ts from 'typescript'
import * as pinia from 'pinia'

class ApiError extends Error { constructor(status) { super('request failed'); this.status = status } }
function harness(request) {
  const exports = {}
  const source = readFileSync(new URL('../src/stores/menuVisibility.ts', import.meta.url), 'utf8')
  vm.runInNewContext(ts.transpileModule(source, {compilerOptions:{module:ts.ModuleKind.CommonJS,target:ts.ScriptTarget.ES2022}}).outputText, {
    exports, require: name => name === 'pinia' ? pinia : name === '@ct/shared' ? {ApiError} : {client:{request}},
  })
  return {store:exports.useMenuVisibilityStore(pinia.createPinia()), groups:exports.menuGroups}
}
test('global visibility defaults on after load; explicit false applies to every menu including settings', async () => {
  const {store, groups} = harness(async () => ({items:{'/':false,'/customers':false,'/settings':false}}))
  assert.equal(store.visible('/customers'),false)
  await store.load()
  assert.equal(store.visible('/customers'),false)
  assert.equal(store.visible('/settings'),false)
  assert.equal(store.visible('/'),false)
  assert.equal(store.visible('/readonly-logs'),true)
  const paths = groups.flatMap(g => g.items.map(i => i[0]))
  assert.equal(new Set(paths).size, paths.length)
  const shell = readFileSync(new URL('../src/components/AppShell.vue',import.meta.url),'utf8')
  for (const path of paths) assert.ok(shell.includes(`"${path}"`),path)
  assert.match(shell,/viewerNav\.filter\(item => menuVisibility\.visible/)
  assert.match(shell,/menuVisibility\.visible\(item\[0\]\) && canVisit/)
  const backend = readFileSync(new URL('../../../../server/internal/dashboard/menu_visibility.go',import.meta.url),'utf8')
  const backendPaths = [...backend.matchAll(/"(\/[^"\s]*)":\s*true/g)].map(m => m[1])
  assert.deepEqual([...paths].sort(), backendPaths.sort())
})
test('failed initial load does not expose menus; unsupported backend is explicit', async () => {
  const failure = harness(async () => {throw new ApiError(500)}).store
  await failure.load(); assert.equal(failure.visible('/'),false); assert.ok(failure.error)
  const legacy = harness(async () => {throw new ApiError(404)}).store
  await legacy.load(); assert.equal(legacy.visible('/'),true); assert.equal(legacy.supported,false); assert.ok(legacy.error)
})
test('save publishes server response and stale in-flight refresh cannot overwrite it', async () => {
  let resolve
  const {store} = harness(async (url,opts) => opts?.method === 'PUT' ? {items:{'/settings':false}} : new Promise(r => {resolve=r}))
  const refresh = store.load()
  const sharedRefresh = store.load()
  await store.save({'/settings':false})
  resolve({items:{'/settings':true}})
  await Promise.all([refresh,sharedRefresh])
  assert.equal(store.visible('/settings'),false)
})
