import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import ts from 'typescript';

const source = readFileSync(new URL('../src/views/AuditsView.vue', import.meta.url), 'utf8');
const functions = source.slice(source.indexOf('function actorLabel('), source.indexOf('function authMethodLabel('));
const compiled = ts.transpileModule(functions, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;
const { actorLabel, targetLabel } = new Function(`${compiled}; return { actorLabel, targetLabel };`)();
const row = (overrides = {}) => ({ operation_type: '', target_type: '', target_id: '', actor_id: '', instance_id: '', instance_name: '', before_summary: '', after_summary: '', source_component: '', ...overrides });

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
