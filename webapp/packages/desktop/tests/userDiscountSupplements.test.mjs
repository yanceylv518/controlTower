import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import ts from 'typescript'
import { computed, effectScope, nextTick, reactive, ref, watch } from 'vue'

const source = readFileSync(new URL('../src/views/UserDiscountSupplementsView.vue', import.meta.url), 'utf8')
const script = source.split('<script setup lang="ts">')[1].split('</script>')[0]
const ast = ts.createSourceFile('page.ts', script, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS)
const printer = ts.createPrinter()
const bare = ast.statements.filter(s => !ts.isImportDeclaration(s))
  .map(s => printer.printNode(ts.EmitHint.Unspecified, s, ast)).join('\n')
const compiled = ts.transpileModule(bare, {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.None },
}).outputText
const flush = async () => { await new Promise(setImmediate); await nextTick() }
const settleUserFilter = async () => { await new Promise(resolve => setTimeout(resolve, 300)); await flush() }
const deferred = () => {
  let resolve, reject
  const promise = new Promise((yes, no) => { resolve = yes; reject = no })
  return { promise, resolve, reject }
}
const from = '2026-10-01T00:00:00+08:00'
const rule = (patch = {}) => ({ id: 1, instance_id: 'a', discount_type: 'user_model',
  subject_id: 7, channel_id: 0, model_name: 'model-a', discount: '0.7', effective_from: from, ...patch })

function create(t) {
  const filters = reactive({ site_id: 'a', loadInstances: async () => {} })
  const writes = [], deletes = [], messages = [], currentReads = [], cleanup = [], userReads = []
  const records = { a: [], b: [] }
  const dashboard = {
    billingDiscounts: async site => ({ items: records[site] || [] }),
    billingUpstreams: async () => ({ channels: [{ models: 'model-a,model-b' }] }),
    billingCurrentDiscounts: async (...args) => { currentReads.push(args); throw Error('current discount unavailable') },
    saveBillingDiscount: async item => {
      writes.push(item)
      const saved = { ...item, id: item.id || 1 }
      records[item.instance_id] = [...records[item.instance_id].filter(r => r.id !== saved.id), saved]
      return saved
    },
    deleteBillingDiscount: async (site, id) => {
      deletes.push({ site, id })
      records[site] = records[site].filter(r => r.id !== id)
    },
  }
  const userDirectory = ref([
    { id: 7, username: 'Alice' }, { id: 8, username: 'Bob' },
  ])
  const passthrough = { users: async (params, signal) => {
    userReads.push({ ...params, signal })
    const included = params.user_ids?.split(',').map(Number), excluded = params.exclude_user_ids?.split(',').map(Number) || []
    const keyword = (params.keyword || '').toLowerCase()
    const matching = userDirectory.value.filter(user => (!included || included.includes(user.id)) && !excluded.includes(user.id) &&
      (!keyword || `${user.username} ${user.display_name || ''} ${user.id}`.toLowerCase().includes(keyword)))
    const offset = params.offset || 0
    return { total: matching.length, items: matching.slice(offset, offset + (params.limit || 50)) }
  } }
  const ElMessage = { success: value => messages.push(value), error: value => messages.push(value) }
  const ElMessageBox = { confirm: async () => {} }
  const document = { addEventListener() {}, removeEventListener() {} }
  const names = ['ref', 'computed', 'reactive', 'watch', 'onMounted', 'onBeforeUnmount',
    'useFiltersStore', 'dashboard', 'passthrough', 'ElMessage', 'ElMessageBox', 'document',
    'setInterval', 'clearInterval']
  const args = [ref, computed, reactive, watch, f => f(), f => cleanup.push(f),
    () => filters, dashboard, passthrough, ElMessage, ElMessageBox, document,
    () => 1, () => {}]
  const scope = effectScope()
  let destroyed = false
  const destroy = () => { if (!destroyed) { destroyed = true; cleanup.forEach(f => f()); scope.stop() } }
  t.after(destroy)
  const page = scope.run(() => new Function(...names, compiled + `;return {
    users, active, rules, catalog, form, target, dialog, saving, saveError, invalid, conflicts,
    rows, names, models, options, now, state, range, userLoading, userError, rulesLoading,
    rulesError, catalogLoading, catalogError, edit, save, remove, loadUsers, loadRules,
    loadSite, loadCatalog, refreshRuleStates, keyword, supplementFilter, visibleUsers, userSummaries, total, hasMoreUsers,
    get siteRevision() { return siteRevision; }
  };`)(...args))
  return { ...page, get siteRevision() { return page.siteRevision }, filters, dashboard, passthrough,
    records, writes, deletes, messages, currentReads, userReads, userDirectory, ElMessageBox, destroy }
}

async function open(c, user = 7, model = 'model-a') {
  await flush()
  c.active.value = c.users.value.find(u => u.id === user)
  await nextTick()
  c.edit(undefined, model)
  Object.assign(c.form, { discount: 0.7, effective_from: from, effective_to: '', remark: '补录测试' })
}

test('open-ended supplements save without querying current site discounts', async t => {
  const c = create(t)
  await open(c)
  assert.equal(c.invalid.value, false)
  await c.save()
  assert.equal(c.writes.length, 1)
  assert.equal(c.writes[0].effective_from, '2026-09-30T16:00:00.000Z')
  assert.equal(c.writes[0].effective_to, undefined)
  assert.equal(c.writes[0].instance_id, 'a')
  assert.equal(c.writes[0].subject_id, 7)
  assert.equal(c.rows.value.length, 1)
  assert.match(c.range(c.rows.value[0]), /长期有效/)
  assert.deepEqual(c.currentReads, [])
})

test('a future end is permitted and an existing end can be cleared', async t => {
  const c = create(t)
  await open(c)
  c.form.effective_to = '2099-01-01T00:00:00+08:00'
  assert.equal(c.invalid.value, false)
  await c.save()
  c.edit(c.rows.value[0])
  c.form.effective_to = ''
  await c.save()
  assert.equal(c.writes.length, 2)
  assert.equal(c.writes[1].id, 1)
  assert.equal(c.rows.value[0].effective_to, undefined)
})

test('invalid multipliers and reversed or invalid dates cannot submit', async t => {
  const c = create(t)
  await open(c)
  for (const discount of [-0.1, 1.1, NaN, Infinity, undefined]) {
    c.form.discount = discount
    await c.save()
  }
  c.form.discount = 0.7
  for (const effective_to of [from, '2026-09-01T00:00:00+08:00', 'invalid']) {
    c.form.effective_to = effective_to
    await c.save()
  }
  c.form.effective_to = ''
  c.form.effective_from = 'invalid'
  await c.save()
  assert.equal(c.writes.length, 0)
})

test('half-open intervals reject overlap but allow adjacent periods', async t => {
  const c = create(t)
  c.records.a = [rule({ effective_to: '2026-10-02T00:00:00+08:00' })]
  await open(c)
  await c.save()
  assert.equal(c.writes.length, 0)
  assert.equal(c.conflicts.value.length, 1)
  c.form.effective_from = '2026-10-02T00:00:00+08:00'
  c.form.model_name = ' model-a '
  assert.equal(c.conflicts.value.length, 0)
  await c.save()
  assert.equal(c.writes.length, 1)
  assert.equal(c.writes[0].model_name, 'model-a')
})

test('a server overlap refreshes existing records and retains the draft', async t => {
  const c = create(t)
  await open(c)
  c.dashboard.saveBillingDiscount = async () => {
    c.records.a = [rule()]
    throw Error('billing_discount_overlap')
  }
  await c.save()
  assert.equal(c.dialog.value, true)
  assert.equal(c.saving.value, false)
  assert.match(c.saveError.value, /重叠/)
  assert.equal(c.conflicts.value.length, 1)
  assert.equal(c.form.remark, '补录测试')
})

test('catalog failure permits entering and saving an exact model name', async t => {
  const c = create(t)
  c.dashboard.billingUpstreams = async () => { throw Error('offline') }
  await open(c, 7, 'manual-model')
  assert.ok(c.catalogError.value)
  await c.save()
  assert.equal(c.writes[0].model_name, 'manual-model')
})

test('load failure blocks saving until the supplement records are recovered', async t => {
  const c = create(t)
  c.dashboard.billingDiscounts = async () => { throw Error('offline') }
  await open(c)
  await c.save()
  assert.equal(c.writes.length, 0)
  c.dashboard.billingDiscounts = async () => ({ items: [] })
  await c.loadRules()
  await c.save()
  assert.equal(c.writes.length, 1)
})

test('late save success cannot close a draft for another user', async t => {
  const c = create(t)
  await open(c)
  const request = deferred()
  c.dashboard.saveBillingDiscount = () => request.promise
  const first = c.save()
  await open(c, 8, 'model-b')
  c.records.a = [rule()]
  request.resolve({ id: 1 })
  await first
  assert.equal(c.dialog.value, true)
  assert.equal(c.target.value.user.id, 8)
  assert.equal(c.form.model_name, 'model-b')
  assert.equal(c.rules.value.length, 1)
  assert.deepEqual(c.messages, [])
})

test('A to B to A ignores an old failure while a newer save is pending', async t => {
  const c = create(t)
  await open(c)
  const old = deferred(), fresh = deferred()
  let count = 0
  c.dashboard.saveBillingDiscount = () => ++count === 1 ? old.promise : fresh.promise
  const first = c.save()
  c.filters.site_id = 'b'
  await flush()
  c.filters.site_id = 'a'
  await open(c, 8, 'model-b')
  const second = c.save()
  assert.equal(count, 2)
  old.reject(Error('old failure'))
  await first
  assert.equal(c.saving.value, true)
  assert.equal(c.saveError.value, '')
  fresh.resolve({ id: 2 })
  await second
  assert.equal(c.saving.value, false)
  assert.equal(c.dialog.value, false)
})

test('an older catalog request cannot overwrite a successful retry', async t => {
  const c = create(t)
  await open(c)
  const old = deferred()
  c.dashboard.billingUpstreams = () => old.promise
  const first = c.loadCatalog(c.siteRevision, 'a')
  c.dashboard.billingUpstreams = async () => ({ channels: [{ models: 'recovered-model' }] })
  await c.loadCatalog(c.siteRevision, 'a')
  old.reject(Error('old failure'))
  await first
  assert.equal(c.catalogError.value, '')
  assert.deepEqual(c.catalog.value, ['recovered-model'])
})

test('switching to an empty site clears pending loading states', async t => {
  const c = create(t)
  await open(c)
  const pending = deferred()
  c.dashboard.billingDiscounts = () => pending.promise
  const first = c.loadRules()
  c.filters.site_id = ''
  await flush()
  assert.equal(c.rulesLoading.value, false)
  assert.equal(c.userLoading.value, false)
  assert.equal(c.catalogLoading.value, false)
  pending.resolve({ items: [rule()] })
  await first
  assert.deepEqual(c.rules.value, [])
})

test('unmount suppresses a pending save result and deletion refreshes the list', async t => {
  const c = create(t)
  await open(c)
  await c.save()
  await c.remove(c.rows.value[0])
  assert.deepEqual(c.deletes, [{ site: 'a', id: 1 }])
  assert.deepEqual(c.rows.value, [])
  c.messages.length = 0
  c.edit(undefined, 'model-b')
  Object.assign(c.form, { effective_from: from })
  const pending = deferred()
  c.dashboard.saveBillingDiscount = () => pending.promise
  const saving = c.save()
  c.destroy()
  pending.resolve({ id: 2 })
  await saving
  assert.deepEqual(c.messages, [])
})

test('late deletion errors do not appear in another site or an unmounted page', async t => {
  for (const change of ['site', 'unmount']) {
    const c = create(t)
    await open(c)
    const pending = deferred()
    c.dashboard.deleteBillingDiscount = () => pending.promise
    const removal = c.remove(rule())
    await flush()
    if (change === 'site') { c.filters.site_id = 'b'; await flush() } else c.destroy()
    pending.reject(Error('old deletion'))
    await removal
    assert.deepEqual(c.messages, [])
  }
})

test('a stale delete confirmation cannot submit after switching away and back', async t => {
  const c = create(t)
  await open(c)
  const confirmation = deferred()
  c.ElMessageBox.confirm = () => confirmation.promise
  const removal = c.remove(rule())
  c.filters.site_id = 'b'
  await flush()
  c.filters.site_id = 'a'
  await flush()
  confirmation.resolve()
  await removal
  assert.deepEqual(c.deletes, [])
})

test('supplement summaries count distinct models and every stored period', async t => {
  const c = create(t)
  c.records.a = [
    rule({ id: 1, effective_to: '2026-10-02T00:00:00+08:00' }),
    rule({ id: 2, effective_from: '2026-10-02T00:00:00+08:00' }),
    rule({ id: 3, model_name: 'model-b', effective_from: '2027-01-01T00:00:00+08:00' }),
  ]
  await open(c)
  assert.equal(c.supplementFilter.value, 'all')
  assert.equal(c.userSummaries.value.get(7).models.size, 2)
  assert.equal(c.userSummaries.value.get(7).records, 3)
  assert.equal(c.userSummaries.value.has(8), false)
  c.supplementFilter.value = 'with'
  await settleUserFilter()
  assert.deepEqual(c.visibleUsers.value.map(user => user.id), [7])
  assert.equal(c.total.value, 1)
  assert.equal(c.userReads.at(-1).user_ids, '7')
  c.supplementFilter.value = 'without'
  await settleUserFilter()
  assert.deepEqual(c.visibleUsers.value.map(user => user.id), [8])
  assert.equal(c.userReads.at(-1).exclude_user_ids, '7')
  c.keyword.value = 'Alice'
  await settleUserFilter()
  assert.deepEqual(c.visibleUsers.value, [])
  assert.equal(c.total.value, 0)
  assert.deepEqual(c.currentReads, [])
})

test('an empty supplemented user set never requests the unfiltered directory', async t => {
  const c = create(t)
  await open(c)
  const reads = c.userReads.length
  c.supplementFilter.value = 'with'
  await settleUserFilter()
  assert.deepEqual(c.visibleUsers.value, [])
  assert.equal(c.total.value, 0)
  assert.equal(c.hasMoreUsers.value, false)
  assert.equal(c.userLoading.value, false)
  assert.equal(c.userReads.length, reads)
  assert.equal(c.active.value, undefined)
})

test('supplement filters apply before pagination and find users beyond the first page', async t => {
  const c = create(t)
  c.userDirectory.value = Array.from({ length: 75 }, (_, index) => ({ id: index + 1, username: `user-${index + 1}` }))
  c.records.a = [rule({ subject_id: 75 })]
  await flush()
  assert.equal(c.users.value.length, 50)
  c.supplementFilter.value = 'with'
  await settleUserFilter()
  assert.deepEqual(c.users.value.map(user => user.id), [75])
  assert.equal(c.total.value, 1)
  c.supplementFilter.value = 'without'
  await settleUserFilter()
  assert.equal(c.users.value.length, 50)
  assert.equal(c.total.value, 74)
  assert.equal(c.hasMoreUsers.value, true)
  await c.loadUsers(true)
  assert.equal(c.users.value.length, 74)
  assert.equal(c.hasMoreUsers.value, false)
  assert.equal(c.users.value.some(user => user.id === 75), false)
  assert.equal(c.userReads.at(-1).offset, 50)
  assert.equal(c.userReads.at(-1).exclude_user_ids, '75')
})

test('changing supplement filters cancels stale user requests and ignores their late failure', async t => {
  const c = create(t)
  c.records.a = [rule()]
  await open(c)
  const old = deferred()
  let oldSignal
  c.passthrough.users = async (params, signal) => {
    if (params.user_ids) { oldSignal = signal; return old.promise }
    return { total: 1, items: [{ id: 8, username: 'Bob' }] }
  }
  c.supplementFilter.value = 'with'
  await settleUserFilter()
  assert.equal(c.userLoading.value, true)
  c.supplementFilter.value = 'without'
  assert.equal(oldSignal.aborted, true)
  await settleUserFilter()
  old.reject(Error('late failure'))
  await flush()
  assert.deepEqual(c.users.value.map(user => user.id), [8])
  assert.equal(c.userError.value, '')
  assert.equal(c.userLoading.value, false)
})

test('saving and deleting supplements refresh filter membership and counts', async t => {
  const c = create(t)
  c.records.a = [rule({ id: 99 })]
  await open(c, 8, 'model-b')
  c.supplementFilter.value = 'without'
  await settleUserFilter()
  assert.deepEqual(c.users.value.map(user => user.id), [8])
  await c.save()
  await settleUserFilter()
  assert.deepEqual(c.visibleUsers.value, [])
  assert.equal(c.total.value, 0)
  assert.equal(c.userSummaries.value.get(8).records, 1)
  c.supplementFilter.value = 'with'
  await settleUserFilter()
  c.active.value = c.users.value.find(user => user.id === 8)
  await nextTick()
  await c.remove(c.rules.value.find(item => item.subject_id === 8))
  await settleUserFilter()
  assert.deepEqual(c.visibleUsers.value.map(user => user.id), [7])
  assert.equal(c.userSummaries.value.has(8), false)
  c.filters.site_id = 'b'
  await flush()
  assert.equal(c.supplementFilter.value, 'all')
  assert.equal(c.userSummaries.value.size, 0)
})
