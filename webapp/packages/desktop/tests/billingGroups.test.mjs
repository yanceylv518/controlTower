import test from 'node:test'
import assert from 'node:assert/strict'
import fs from 'node:fs'
import ts from 'typescript'
import { computed, ref, watch, nextTick } from 'vue'

const source = fs.readFileSync(new URL('../src/components/BillingRecordsView.vue', import.meta.url), 'utf8')
const logic = source.slice(source.indexOf('const records = computed'), source.indexOf('const formatTime ='))
const compiled = ts.transpile(logic, { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.None })
const create = new Function('computed', 'ref', 'watch', 'state', 'props', 'filters', `${compiled}; return { userSearch, billDateRange, userPage, expandedUsers, userGroups, matchingUsers, matchingBillCount, visibleGroups, toggleUser };`)
const bill = (id, site, user, name, from, to, updated, mismatch = 0) => ({ id, instance_id: site, user_id: user, user_name: name, job_type: 'user_statement', range_from: from, range_to: to, updated_at: updated, mismatch_rows: mismatch })
function page(items) { return create(computed, ref, watch, { data: ref({ items }) }, { billType: 'user' }, { site_id: 'site' }) }

test('billing groups preserve same user IDs on distinct sites and retain every bill', () => {
  const p = page([
    bill('a', 'one', 7, 'Alice', '2026-08-01', '2026-09-01', '2026-09-02'),
    bill('b', 'one', 7, 'Alice', '2026-09-01', '2026-10-01', '2026-09-03', 2),
    bill('c', 'two', 7, 'Bob', '2026-09-01', '2026-10-01', '2026-09-04'),
  ])
  assert.equal(p.userGroups.value.length, 2)
  assert.equal(p.matchingBillCount.value, 3)
  const alice = p.userGroups.value.find(g => g.site === 'one')
  assert.deepEqual(alice.bills.map(b => b.id), ['b', 'a'])
  assert.equal(alice.reviewCount, 1)
  p.userSearch.value = 'alice'
  assert.equal(p.matchingUsers.value.length, 1)
  p.toggleUser(alice.key)
  assert.deepEqual(p.expandedUsers.value, [alice.key])
  p.toggleUser(alice.key)
  assert.deepEqual(p.expandedUsers.value, [])
})

test('billing date filter includes selected end day, excludes adjacent nonoverlapping periods', () => {
  const iso = (month, day) => new Date(2026, month - 1, day).toISOString()
  const p = page([
    bill('before', 'one', 7, 'Alice', iso(8, 1), iso(9, 1), iso(9, 2)),
    bill('inside', 'one', 7, 'Alice', iso(9, 1), iso(9, 2), iso(9, 3), 1),
    bill('after', 'one', 7, 'Alice', iso(9, 2), iso(9, 3), iso(9, 4)),
  ])
  p.billDateRange.value = [new Date(2026, 8, 1), new Date(2026, 8, 1)]
  assert.deepEqual(p.matchingUsers.value[0].bills.map(b => b.id), ['inside'])
  assert.equal(p.matchingUsers.value[0].latest.id, 'inside')
  assert.equal(p.matchingBillCount.value, 1)
})

test('billing pagination recovers when a search narrows the matching users', async () => {
  const p = page(Array.from({ length: 25 }, (_, i) => bill(String(i), 'site', i + 1, `Customer ${i}`, '2026-09-01', '2026-09-02', '2026-09-03')))
  p.userPage.value = 2
  assert.equal(p.visibleGroups.value.length, 5)
  p.userSearch.value = 'Customer 24'
  await nextTick()
  assert.equal(p.userPage.value, 1)
  assert.equal(p.visibleGroups.value.length, 1)
  assert.equal(p.visibleGroups.value[0].name, 'Customer 24')
})
