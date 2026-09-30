import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import ts from 'typescript';

const source = readFileSync(new URL('../src/views/AuditsView.vue', import.meta.url), 'utf8');
const functions = source.slice(source.indexOf('function actorLabel('), source.indexOf('function authMethodLabel('));
const compiled = ts.transpileModule(functions, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;
const { actorLabel, targetLabel } = new Function(`${compiled}; return { actorLabel, targetLabel };`)();
const row = (overrides = {}) => ({ operation_type: '', target_type: '', target_id: '', actor_id: '', instance_id: '', instance_name: '', before_summary: '', after_summary: '', source_component: '', ...overrides });

test('type filter precedes intelligent search and applies without submitting other drafts', () => {
  const template = source.slice(source.indexOf('<template>'));
  assert.ok(template.indexOf('v-model="draft.operation_type"') < template.indexOf('v-model="draft.q"'));
  assert.doesNotMatch(template, /draft\.search_mode|aria-label="搜索方式"/);
  assert.match(template, /@change="applyOperationType"/);
  assert.match(source, /applied\.value = \{ \.\.\.applied\.value, operation_type: draft\.operation_type \}/);
  assert.match(source, /search_mode: "smart" as const/);
  assert.match(source, /actor_options: true, actor: value\.trim\(\) \|\| undefined, operation_type: applied\.value\.operation_type/);
});

test('intelligent keywords validate phrases and bound search complexity', () => {
  const block = source.slice(source.indexOf('function validSearchTerms('), source.indexOf('function applyOperationType('));
  const compiled = ts.transpileModule(block, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;
  const valid = new Function(`${compiled}; return validSearchTerms;`)();
  assert.equal(valid('gpt-4 香港 7'), true);
  assert.equal(valid('"香港 备用" gpt-4'), true);
  assert.equal(valid('"未闭合'), false);
  assert.equal(valid('""'), false);
  assert.equal(valid('a b c d e f g h i'), false);
  assert.equal(valid(''), true);
});

test('selecting a type resets history but preserves unapplied inputs and applied search', () => {
  const block = source.slice(source.indexOf('function applyOperationType('), source.indexOf('function resetTimeRange('));
  const compiled = ts.transpileModule(block, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;
  const expanded = { value: ['old-row'] };
  const applied = { value: { q: 'gpt-4', operation_type: '', actor: 'alice' } };
  const draft = { q: 'new input', operation_type: 'billing.price_update', actor: 'bob' };
  let resets = 0;
  const apply = new Function('expandedRows', 'applied', 'draft', 'history', `${compiled}; return applyOperationType;`)(expanded, applied, draft, { reset() { resets++; } });
  apply();
  assert.deepEqual(applied.value, { q: 'gpt-4', operation_type: 'billing.price_update', actor: 'alice' });
  assert.equal(draft.q, 'new input');
  assert.deepEqual(expanded.value, []);
  assert.equal(resets, 1);
  draft.operation_type = '';
  apply();
  assert.equal(applied.value.operation_type, '');
  assert.equal(resets, 2);
});

test('actor roles omit manual prefix without claiming an unverified identity', () => {
  assert.equal(actorLabel({ actor_type: 'human', actor_role: 'admin' }), '管理员');
  assert.equal(actorLabel({ actor_type: 'human', actor_role: 'viewer' }), '查看账号');
  assert.equal(actorLabel({ actor_type: 'service_token' }), '服务令牌');
  assert.equal(actorLabel({ actor_type: 'human' }), '身份未验证');
});

test('account targets use the changed account instead of the administrator name', () => {
  assert.equal(targetLabel(row({ operation_type: 'auth.account_update', target_id: '9', after_summary: JSON.stringify({ actor_username: 'operator', account: { username: 'reader', display_name: '访客' } }) })), '账号 · 访客（reader）（ID：9）');
  assert.equal(targetLabel(row({ operation_type: 'auth.login', target_id: 'reader', actor_id: 'unknown', after_summary: '{"request":{"username":"reader","password":"[redacted]"}}' })), '账号 · reader');
});

test('targets retain identifiers and deleted names while rendering global and site configuration clearly', () => {
  assert.equal(targetLabel(row({ operation_type: 'settings.update', target_id: 'global' })), '全局系统设置');
  assert.equal(targetLabel(row({ operation_type: 'tuning.policy_update', instance_name: '演示站点' })), '调权策略 · 演示站点');
  assert.equal(targetLabel(row({ operation_type: 'http.tuning.tuning.channels.post', instance_name: '演示站点', target_id: 'inst-demo' })), '渠道基础配置 · 演示站点');
  assert.equal(targetLabel(row({ operation_type: 'billing.upstream.delete', target_id: '2', before_summary: '{"name":"上游A"}', after_summary: '{}' })), '账单上游 · 上游A（ID：2）');
  assert.equal(targetLabel(row({ target_type: 'channel', target_id: '7', after_summary: 'malformed' })), '渠道 #7');
});
