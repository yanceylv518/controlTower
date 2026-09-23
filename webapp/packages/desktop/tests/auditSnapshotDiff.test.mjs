import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import ts from 'typescript';
import { computed } from 'vue';

const source = readFileSync(new URL('../src/components/AuditSnapshotDiff.vue', import.meta.url), 'utf8');
const script = source.match(/<script setup lang="ts">([^]*?)<\/script>/)[1];
const compiled = ts.transpileModule(script.replace(/^import .*;\r?\n/gm, ''), {
  compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ES2022 },
}).outputText;
const createDiff = new Function(
  'computed',
  'defineProps',
  `${compiled}\nreturn { rows, changedCount, canCompare, fieldLabel, requiresScroll, displayValue };`,
);

function compare(before, after) {
  return createDiff(computed, () => ({ before, after }));
}

test('only added, removed, and changed fields remain in a complete comparison', () => {
  const result = compare(
    JSON.stringify({ mode: 'observe', policy: { unchanged: 30, updated: 1, removed: true } }),
    JSON.stringify({ mode: 'observe', policy: { unchanged: 30, updated: 2, added: 'yes' } }),
  );

  assert.deepEqual(
    result.rows.value.map((row) => [row.path.join('/'), row.change]),
    [
      ['policy/updated', 'changed'],
      ['policy/removed', 'removed'],
      ['policy/added', 'added'],
    ],
  );
  assert.equal(result.changedCount.value, 3);
});

test('identical snapshots produce no comparison rows', () => {
  const snapshot = JSON.stringify({ mode: 'observe', policy: { window_minutes: 30 } });
  const result = compare(snapshot, snapshot);

  assert.equal(result.canCompare.value, true);
  assert.deepEqual(result.rows.value, []);
  assert.equal(result.changedCount.value, 0);
});

test('account audit envelopes compare only actual changed account fields', () => {
  const result = compare(
    JSON.stringify({ username: 'reader', role: 'viewer', enabled: true }),
    JSON.stringify({ actor_username: 'admin', account: { username: 'reader', role: 'viewer', enabled: false } }),
  );
  assert.deepEqual(result.rows.value.map(row => row.path), [['enabled']]);
  assert.equal(result.changedCount.value, 1);
});

test('known enum values render readable labels without translating arbitrary text', () => {
  const result = compare('', '{}');
  assert.equal(result.displayValue('admin', true, ['role']), '管理员');
  assert.equal(result.displayValue('admin', true, ['username']), 'admin');
  assert.equal(result.displayValue('[redacted]', true, ['password']), '已隐藏敏感信息');
  assert.equal(result.displayValue(null, true), '空值');
});

test('known audit field names render as Chinese labels', () => {
  const result = compare(
    '{"weight":26,"priority":2,"scope_site":"site-a"}',
    '{"weight":46,"priority":3,"scope_site":"site-b"}',
  );

  assert.deepEqual(result.rows.value.map((row) => result.fieldLabel(row.path)), ['权重', '优先级', '站点范围']);
});

test('unknown snapshot keys retain a Chinese field marker', () => {
  const result = compare('{"tenant_option":1}', '{"tenant_option":2}');

  assert.equal(result.fieldLabel(result.rows.value[0].path), '字段：tenant_option');
});

test('one-sided snapshots remain context instead of being reported as changes', () => {
  const result = compare('', JSON.stringify({ request: { limit: 20 } }));

  assert.equal(result.canCompare.value, false);
  assert.deepEqual(result.rows.value.map((row) => [row.path.join('/'), row.change]), [
    ['request/limit', 'context'],
  ]);
  assert.equal(result.changedCount.value, 0);
});

test('large field comparisons enable an internal scroll region after eight rows', () => {
  const snapshot = (count) => JSON.stringify(Object.fromEntries(
    Array.from({ length: count }, (_, index) => [`field_${index}`, index]),
  ));

  assert.equal(compare('', snapshot(8)).requiresScroll.value, false);
  assert.equal(compare('', snapshot(9)).requiresScroll.value, true);
  assert.match(source, /class="audit-snapshot-rows"[\s\S]*?is-scrollable/);
  assert.match(source, /\.audit-snapshot-rows\.is-scrollable\s*\{[^}]*max-height:\s*280px;[^}]*overflow-y:\s*auto;/);
});
